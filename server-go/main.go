package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/time/rate"

	ywebsocket "github.com/reearth/ygo/provider/websocket"
)

// Version is injected at build time via -ldflags "-X main.Version=vX.Y.Z".
var Version = "dev"

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[board] ")

	cfg := LoadConfig()

	// ── Insecure-mode guard (finding 4 — MED) ────────────────────────────────
	// When auth is OFF an unauthenticated board server must never be reachable
	// off the local machine. Mirror Node index.mjs:84: force loopback binding
	// unless the operator has explicitly opted out with BOARD_ALLOW_INSECURE=1.
	if !cfg.Auth.Enabled() && !cfg.AllowInsecure {
		override := loopbackAddr(cfg.ListenAddr)
		log.Printf("auth:        OFF — binding to %s (set BOARD_ALLOW_INSECURE=1 to bind on all interfaces)", override)
		cfg.ListenAddr = override
	}

	// Select the persistence adapter: bucket when the storage seam is configured,
	// on-disk fallback otherwise (local dev).
	var adapter *Adapter
	if cfg.Storage.Configured() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		store, err := NewS3Store(ctx, cfg.Storage)
		cancel()
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
		adapter = NewBucketPersistence(store, cfg.Storage.Prefix, cfg.Debounce)
	} else {
		if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
			log.Fatalf("data dir: %v", err)
		}
		adapter = NewDiskPersistence(cfg.DataDir, cfg.Debounce)
	}

	// ygo provider/websocket Server — speaks the y-websocket protocol (sync
	// step1/2, update, awareness) that board-ui's createWebsocketProvider uses.
	// The server owns the upgrade (gorilla/websocket internally); we only supply
	// auth + origin policy, exactly like wede wires its DocServer.
	srv := ywebsocket.NewServerWithPersistence(adapter)
	adapter.SetProvider(srv) // enable debounced snapshot write-back

	// ── DoS caps (finding 1 — HIGH) ──────────────────────────────────────────
	// Wire all four limits that the Node server enforces but the Go server
	// previously left at 0 (unlimited). Sane defaults are configured in
	// LoadConfig(); operators can override via env.
	srv.MaxConnections = cfg.DoS.MaxConnections
	srv.MaxPeersPerRoom = cfg.DoS.MaxPeersPerRoom
	if cfg.DoS.MessageRateLimit > 0 {
		srv.MessageRateLimit = rate.Limit(cfg.DoS.MessageRateLimit)
	}
	srv.MaxAwarenessBytesPerRoom = cfg.DoS.MaxAwarenessBytesPerRoom

	if len(cfg.AllowedOrigins) > 0 {
		srv.AllowedOrigins = cfg.AllowedOrigins
	}

	// ── ReadOnly hub (finding 5 — MED) ───────────────────────────────────────
	// The hub fans out live doc updates to ro WebSocket peers. Must be created
	// before OnLoadDocument is wired so all rooms share the same hub.
	hub := newReadOnlyHub()

	// ── OnLoadDocument: blob pruning + ro hub (findings 5 & 6) ───────────────
	// Wire per-room observers immediately after each room is bootstrapped from
	// persistence. wireSecurity returns the hook function (defined in security.go).
	srv.OnLoadDocument = wireSecurity(cfg, hub)

	// ── Auth (findings 2, 3 — HIGH) ──────────────────────────────────────────
	// NewAuthenticator enforces: exp REQUIRED, room REQUIRED, max-TTL bound,
	// and records the ro flag for IsReadOnly queries below.
	auth := NewAuthenticator(cfg.Auth)
	if auth.Enabled() {
		srv.AuthFunc = auth.Check
	}

	// y-websocket endpoint: the client connects to `${url}/${room}`, so with the
	// host default VITE_BOARD_WS_URL=wss://board.vulos.org/ws the room arrives as
	// the trailing path segment of /ws/<room>. ygo reads {room} from the path.
	//
	// ── Read-only gate (finding 5 — MED) ─────────────────────────────────────
	// Wrap the ygo handler: requests whose verified token carries ro=true are
	// routed to ServeReadOnly (which drops client update messages) rather than
	// to ygo's normal handler (which would let the peer write to the doc).
	mux := http.NewServeMux()
	mux.Handle("/ws/{room}", buildWSHandler(srv, auth, adapter, hub))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("vulos board sync server — ok\n"))
	})

	// Structured startup log of the operating mode.
	log.Printf("vulos board sync server %s", Version)
	log.Printf("listen:      %s", cfg.ListenAddr)
	log.Printf("ws endpoint: /ws/{room}")
	log.Printf("persistence: %s", adapter.Describe())
	if auth.Enabled() {
		log.Printf("auth:        ON (exp+room required, max-TTL %ds, ro-claim enforced)", cfg.Auth.MaxTokenTTL)
	} else {
		log.Printf("auth:        OFF — WARNING: no BOARD_AUTH_SECRET set, all connections accepted (dev only)")
	}
	log.Printf("dos caps:    max-conns=%d max-peers-per-room=%d msg-rate=%.0f/s awareness=%dMiB blobs=%dMiB",
		cfg.DoS.MaxConnections, cfg.DoS.MaxPeersPerRoom,
		cfg.DoS.MessageRateLimit,
		cfg.DoS.MaxAwarenessBytesPerRoom>>20,
		cfg.DoS.MaxBlobBytes>>20,
	)
	if len(cfg.AllowedOrigins) > 0 {
		log.Printf("origins:     %v", cfg.AllowedOrigins)
	} else {
		log.Printf("origins:     same-origin check (set BOARD_ALLOWED_ORIGINS behind the OS gateway)")
	}

	server := &http.Server{Addr: cfg.ListenAddr, Handler: mux}

	// Graceful shutdown: stop accepting, then flush pending snapshots.
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Printf("shutting down…")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		adapter.Stop() // final synchronous snapshot flush
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// buildWSHandler returns the /ws/{room} handler. When auth is enabled,
// ro-flagged connections are routed to the read-only handler so client-sent
// update messages are dropped. All other requests go to the ygo srv directly.
func buildWSHandler(srv *ywebsocket.Server, auth *Authenticator, adapter *Adapter, hub *ReadOnlyHub) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.Enabled() && auth.IsReadOnly(r) {
			room := r.PathValue("room")
			defer auth.Release(r)
			ServeReadOnly(w, r, room, adapter, hub)
			return
		}
		defer auth.Release(r)
		srv.ServeHTTP(w, r)
	})
}
