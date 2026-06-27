package main

import (
	"os"
	"strings"
	"time"
)

// Config is the board sync server's runtime configuration, loaded entirely from
// the environment so it slots behind the Vulos OS gateway like the other
// products (no flags required).
type Config struct {
	// ListenAddr is the HTTP listen address (BOARD_LISTEN_ADDR, default ":8080").
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

	// Auth carries the optional token-verification secret.
	Auth AuthConfig

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

// AuthConfig holds the websocket-upgrade token secret. When Secret is empty,
// auth is disabled (dev mode) and every connection is accepted.
type AuthConfig struct {
	Secret []byte
}

// Enabled reports whether token verification is active.
func (a AuthConfig) Enabled() bool { return len(a.Secret) > 0 }

// defaultDebounce matches wede's collabdoc write-back debounce.
const defaultDebounce = 600 * time.Millisecond

// LoadConfig reads the server configuration from the environment.
func LoadConfig() Config {
	return Config{
		ListenAddr:     envDefault("BOARD_LISTEN_ADDR", ":8080"),
		DataDir:        envDefault("BOARD_DATA_DIR", "./.board-data"),
		AllowedOrigins: splitList(os.Getenv("BOARD_ALLOWED_ORIGINS")),
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
			Secret: []byte(os.Getenv("BOARD_AUTH_SECRET")),
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
