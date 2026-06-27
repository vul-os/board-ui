package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Authenticator validates the `?token=` query parameter on the WebSocket
// upgrade. It is the board side of seam A: the Vulos OS gateway / CP is the
// intended token minter — it holds BOARD_AUTH_SECRET and stamps a signed token
// into the board iframe's connection URL. When no secret is configured the
// server runs open (dev) and every connection is accepted.
//
// Token shape (HMAC, mintable by any holder of the shared secret):
//
//	token = base64url(payloadJSON) + "." + base64url(HMAC_SHA256(secret, base64url(payloadJSON)))
//
// where payloadJSON is, e.g. {"exp":1750000000,"room":"userid:default"}.
//   - exp  (optional): unix seconds; the token is rejected once it passes.
//   - room (optional): when non-empty it must equal the room being joined, so a
//     token can be scoped to a single board.
//
// Both parts are compared with crypto/subtle.ConstantTimeCompare. Verifying only
// a signature (no third-party PKI) keeps this the simplest real option; a JWT /
// BOARD_JWT_PUBLIC_KEY verifier could be slotted in behind the same interface.
type Authenticator struct {
	secret []byte
}

// NewAuthenticator builds an authenticator from the configured secret. When the
// secret is empty, Enabled() is false and Check always allows.
func NewAuthenticator(cfg AuthConfig) *Authenticator {
	return &Authenticator{secret: cfg.Secret}
}

// Enabled reports whether token verification is active.
func (a *Authenticator) Enabled() bool { return len(a.secret) > 0 }

// Check is the ygo Server.AuthFunc: it returns true to allow the upgrade. With
// no secret configured it allows everything (dev). Otherwise it requires a valid
// `?token=` whose optional room claim matches the requested room.
func (a *Authenticator) Check(r *http.Request) bool {
	if !a.Enabled() {
		return true
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		return false
	}
	return a.verify(token, roomFromRequest(r))
}

// verify checks the token's HMAC signature and optional exp/room claims.
func (a *Authenticator) verify(token, room string) bool {
	payloadB64, sigB64, ok := strings.Cut(token, ".")
	if !ok || payloadB64 == "" || sigB64 == "" {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(payloadB64))
	expected := mac.Sum(nil)
	if subtle.ConstantTimeCompare(sig, expected) != 1 {
		return false
	}
	// Signature is valid; enforce optional claims.
	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return false
	}
	var claims struct {
		Exp  int64  `json:"exp"`
		Room string `json:"room"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return false
	}
	if claims.Exp > 0 && time.Now().Unix() > claims.Exp {
		return false
	}
	if claims.Room != "" && claims.Room != room {
		return false
	}
	return true
}

// roomFromRequest extracts the room the same way ygo's Server does: the {room}
// path value, falling back to the last path segment.
func roomFromRequest(r *http.Request) string {
	if v := r.PathValue("room"); v != "" {
		return v
	}
	p := strings.TrimRight(r.URL.Path, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
