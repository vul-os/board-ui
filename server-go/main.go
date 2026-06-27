package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	ywebsocket "github.com/reearth/ygo/provider/websocket"
)

// Version is injected at build time via -ldflags "-X main.Version=vX.Y.Z".
var Version = "dev"

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[board] ")

	cfg := LoadConfig()

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

	if len(cfg.AllowedOrigins) > 0 {
		srv.AllowedOrigins = cfg.AllowedOrigins
	}

	auth := NewAuthenticator(cfg.Auth)
	if auth.Enabled() {
		srv.AuthFunc = auth.Check
	}

	mux := http.NewServeMux()
	// y-websocket endpoint: the client connects to `${url}/${room}`, so with the
	// host default VITE_BOARD_WS_URL=wss://board.vulos.org/ws the room arrives as
	// the trailing path segment of /ws/<room>. ygo reads {room} from the path.
	mux.Handle("/ws/{room}", srv)
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
		log.Printf("auth:        ON (token verified against BOARD_AUTH_SECRET)")
	} else {
		log.Printf("auth:        OFF — WARNING: no BOARD_AUTH_SECRET set, all connections accepted (dev only)")
	}
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
