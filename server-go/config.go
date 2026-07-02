package main

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the board sync server's runtime configuration, loaded entirely from
// the environment so it slots behind the Vulos OS gateway like the other
// products (no flags required).
type Config struct {
	// ListenAddr is the HTTP listen address (BOARD_LISTEN_ADDR, default ":8080").
	// When auth is disabled and BOARD_ALLOW_INSECURE is not set, this is
	// overridden to "127.0.0.1:<port>" at startup so the unauthenticated server
	// is never reachable off-host (mirrors Node index.mjs:84).
	ListenAddr string

	// DataDir is the local-disk snapshot root used by the DiskPersistence
	// fallback when no object store is configured (BOARD_DATA_DIR).
	DataDir string

	// AllowedOrigins is the CORS allow-list for the WebSocket upgrade
	// (BOARD_ALLOWED_ORIGINS, space- or comma-separated). Empty => ygo falls
	// back to a same-origin check (Origin must equal Host); non-browser clients
	// that omit Origin are always allowed.
	AllowedOrigins []string

	// Storage is the object-store binding (the Vulos storage seam). When
	// Configured() is false the server uses the on-disk fallback.
	Storage StorageConfig

	// Auth carries the optional token-verification secret and policy.
	Auth AuthConfig

	// DoS carries the server-wide and per-room resource caps (all default to
	// sane values; set to 0 in env to restore unlimited, except for
	// MaxBlobBytes which is always applied).
	DoS DoSConfig

	// AllowInsecure permits auth-OFF binding on all interfaces when true.
	// Set BOARD_ALLOW_INSECURE=1 to opt in explicitly (local-dev only).
	// Without this, an unauthenticated server is forced to 127.0.0.1.
	AllowInsecure bool

	// Debounce is how long StoreUpdate waits after the last edit before flushing
	// a full snapshot to storage.
	Debounce time.Duration
}

// StorageConfig mirrors the Vulos storage-seam env contract (VULOS_STORAGE_*),
// one field per X-Vulos-Storage-* header, exactly like vulos' storage.Resolution
// and vulos-office's blob seam.
type StorageConfig struct {
	Endpoint     string // S3 URL (scheme://host[:port]) or bare host[:port]; empty => disk fallback
	Bucket       string
	Prefix       string // key prefix within the bucket, normalised to a trailing slash (e.g. "board-app/")
	Region       string
	AccessKey    string
	SecretKey    string
	SessionToken string // optional (STS / temporary credentials)
}

// Configured reports whether the storage seam points at a usable object store.
func (s StorageConfig) Configured() bool {
	return s.Endpoint != "" && s.Bucket != ""
}

// AuthConfig holds the websocket-upgrade token secret and policy. When Secret
// is empty, auth is disabled (dev mode) and every connection is accepted.
type AuthConfig struct {
	Secret []byte

	// MaxTokenTTL is the maximum permitted token lifetime in seconds
	// (BOARD_MAX_TOKEN_TTL_SECONDS, default 3600). A token whose exp sits
	// further than MaxTokenTTL seconds in the future is rejected even if
	// its HMAC is valid — prevents over-long-lived URL-borne tokens.
	// Set to 0 to disable the bound.
	MaxTokenTTL int64
}

// Enabled reports whether token verification is active.
func (a AuthConfig) Enabled() bool { return len(a.Secret) > 0 }

// DoSConfig holds the server-wide and per-room resource caps that guard
// against denial-of-service. Each field mirrors its Node counterpart in
// server/index.mjs and maps to a ygo Server field of the same meaning.
type DoSConfig struct {
	// MaxConnections is the server-wide cap on simultaneous WebSocket peers
	// (BOARD_MAX_CONNS, default 1024). Maps to ygo Server.MaxConnections.
	MaxConnections int

	// MaxPeersPerRoom is the per-room cap on simultaneous WebSocket peers
	// (BOARD_MAX_ROOM_CONNS, default 64). Maps to ygo Server.MaxPeersPerRoom.
	MaxPeersPerRoom int

	// MaxRooms caps the total number of rooms the server will hold in memory
	// at once (BOARD_MAX_ROOMS, default 4096). Maps to ygo Server.MaxRooms.
	// Defense-in-depth: without it a flood of distinct room names could
	// exhaust memory even under the connection caps. Set to 0 for unlimited.
	MaxRooms int

	// MessageRateLimit is the sustained inbound-message rate per peer in
	// messages/second (BOARD_MESSAGE_RATE, default 50). Maps to
	// ygo Server.MessageRateLimit. Set to 0 to disable.
	MessageRateLimit float64

	// MaxAwarenessBytesPerRoom caps the cumulative awareness state in one room
	// (BOARD_MAX_AWARENESS_BYTES, default 100 MiB). Maps to
	// ygo Server.MaxAwarenessBytesPerRoom.
	MaxAwarenessBytesPerRoom int64

	// MaxBlobBytes is the max size of a single dataURL in the "files" YMap
	// (BOARD_MAX_BLOB_BYTES, default 10 MiB). Entries that exceed this are
	// pruned server-side via a YMap observer wired in OnLoadDocument.
	MaxBlobBytes int64
}

// defaultDebounce matches wede's collabdoc write-back debounce.
const defaultDebounce = 600 * time.Millisecond

// DoS cap defaults — mirror Node's server/index.mjs constants.
const (
	defaultMaxConnections           = 1024
	defaultMaxPeersPerRoom          = 64
	defaultMaxRooms                 = 4096
	defaultMessageRateLimit         = 50.0      // msgs/sec per peer
	defaultMaxAwarenessBytesPerRoom = 100 << 20 // 100 MiB
	defaultMaxBlobBytes             = 10 << 20  // 10 MiB (matches Node MAX_BLOB_BYTES)
	defaultMaxTokenTTL              = 3600      // 1 hour (matches Node MAX_TOKEN_TTL)
)

// LoadConfig reads the server configuration from the environment.
func LoadConfig() Config {
	return Config{
		ListenAddr:     envDefault("BOARD_LISTEN_ADDR", ":8080"),
		DataDir:        envDefault("BOARD_DATA_DIR", "./.board-data"),
		AllowedOrigins: splitList(os.Getenv("BOARD_ALLOWED_ORIGINS")),
		AllowInsecure:  os.Getenv("BOARD_ALLOW_INSECURE") == "1",
		Storage: StorageConfig{
			Endpoint:     os.Getenv("VULOS_STORAGE_ENDPOINT"),
			Bucket:       os.Getenv("VULOS_STORAGE_BUCKET"),
			Prefix:       normalizePrefix(os.Getenv("VULOS_STORAGE_PREFIX")),
			Region:       envDefault("VULOS_STORAGE_REGION", "us-east-1"),
			AccessKey:    os.Getenv("VULOS_STORAGE_ACCESS_KEY"),
			SecretKey:    os.Getenv("VULOS_STORAGE_SECRET_KEY"),
			SessionToken: os.Getenv("VULOS_STORAGE_SESSION_TOKEN"),
		},
		Auth: AuthConfig{
			Secret:      []byte(os.Getenv("BOARD_AUTH_SECRET")),
			MaxTokenTTL: envInt64("BOARD_MAX_TOKEN_TTL_SECONDS", defaultMaxTokenTTL),
		},
		DoS: DoSConfig{
			MaxConnections:           envInt("BOARD_MAX_CONNS", defaultMaxConnections),
			MaxPeersPerRoom:          envInt("BOARD_MAX_ROOM_CONNS", defaultMaxPeersPerRoom),
			MaxRooms:                 envInt("BOARD_MAX_ROOMS", defaultMaxRooms),
			MessageRateLimit:         envFloat64("BOARD_MESSAGE_RATE", defaultMessageRateLimit),
			MaxAwarenessBytesPerRoom: envInt64("BOARD_MAX_AWARENESS_BYTES", defaultMaxAwarenessBytesPerRoom),
			MaxBlobBytes:             envInt64("BOARD_MAX_BLOB_BYTES", defaultMaxBlobBytes),
		},
		Debounce: defaultDebounce,
	}
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envInt reads an integer env var, returning def when unset or invalid.
func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

// envInt64 reads an int64 env var, returning def when unset or invalid.
func envInt64(key string, def int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return def
	}
	return n
}

// envFloat64 reads a float64 env var, returning def when unset or invalid.
func envFloat64(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 {
		return def
	}
	return f
}

// splitList splits a space- or comma-separated env value into trimmed entries.
func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	fields := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// normalizePrefix trims slashes and re-adds a single trailing one (empty stays
// empty), matching vulos' storage.Resolution.WithPrefix.
func normalizePrefix(p string) string {
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	return p + "/"
}

// loopbackAddr replaces the host part of addr with 127.0.0.1. addr must be a
// ":port" or "host:port" string as used by net/http Server.Addr.
func loopbackAddr(addr string) string {
	// Find the last colon — handles bare ":8080" and "0.0.0.0:8080".
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return "127.0.0.1" + addr
	}
	return "127.0.0.1" + addr[i:]
}
