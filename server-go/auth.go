package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
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
//   - exp  (REQUIRED): unix seconds; tokens without exp are rejected; tokens
//     whose exp lies further than MaxTokenTTL seconds in the future are also
//     rejected (prevents over-long-lived URL-borne tokens). Matches Node's
//     stricter SECURE-mode policy (index.mjs:257-263).
//   - room (REQUIRED): must be non-empty and must equal the room being joined.
//     A room-less token would otherwise act as a master key valid for any room.
//   - ro   (optional): when true the connection is admitted read-only — the
//     peer may receive state and live updates but write messages are dropped.
//     IsReadOnly(r) reports the ro flag after a successful Check.
//
// Both token parts are compared with crypto/subtle.ConstantTimeCompare.
// Verifying only a signature (no third-party PKI) keeps this the simplest real
// option; a JWT / BOARD_JWT_PUBLIC_KEY verifier could be slotted in behind the
// same interface.
type Authenticator struct {
	secret  []byte
	maxTTL  int64    // seconds; 0 = no TTL bound
	roConns sync.Map // *http.Request → struct{}: tracks ro-flagged connections
}

// NewAuthenticator builds an authenticator from the configured secret. When the
// secret is empty, Enabled() is false and Check always allows.
func NewAuthenticator(cfg AuthConfig) *Authenticator {
	return &Authenticator{
		secret: cfg.Secret,
		maxTTL: cfg.MaxTokenTTL,
	}
}

// Enabled reports whether token verification is active.
func (a *Authenticator) Enabled() bool { return len(a.secret) > 0 }

// Check is the ygo Server.AuthFunc: it returns true to allow the upgrade. With
// no secret configured it allows everything (dev). Otherwise it requires a valid
// `?token=` whose room claim matches the requested room. On success, if the
// token carries ro=true, the request is recorded in roConns so IsReadOnly(r)
// returns true; call Release(r) once the connection closes.
func (a *Authenticator) Check(r *http.Request) bool {
	if !a.Enabled() {
		return true
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		return false
	}
	ro, ok := a.verify(token, roomFromRequest(r))
	if !ok {
		return false
	}
	if ro {
		a.roConns.Store(r, struct{}{})
	}
	return true
}

// Verify parses and HMAC-verifies the request's `?token=` and returns its ro
// claim, WITHOUT the roConns side-effect that Check records. It is the routing
// primitive used by buildWSHandler to decide, BEFORE dispatch, whether a
// connection is read-only, read/write, or must be rejected — closing the
// broken-access-control hole where IsReadOnly was consulted before ygo's
// AuthFunc (Check) had run, so the ro flag was never observed.
//
// Returns:
//   - (false, true)  when auth is disabled (dev mode): allow, read/write.
//   - (ro, true)     for a valid token: ro reflects the verified ro claim.
//   - (false, false) for a missing/invalid/expired/wrong-room token: reject.
//
// Room scope, exp requirement, max-TTL bound, and constant-time signature
// comparison are all enforced (via verify), identical to Check.
func (a *Authenticator) Verify(r *http.Request) (ro bool, ok bool) {
	if !a.Enabled() {
		return false, true
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		return false, false
	}
	return a.verify(token, roomFromRequest(r))
}

// IsReadOnly reports whether the request carried an ro=true claim. Must be
// called only after Check returned true.
func (a *Authenticator) IsReadOnly(r *http.Request) bool {
	_, ok := a.roConns.Load(r)
	return ok
}

// Release removes the request's ro entry; call once the connection closes to
// avoid a memory leak. Safe to call even if the request was not ro-flagged.
func (a *Authenticator) Release(r *http.Request) {
	a.roConns.Delete(r)
}

// verify checks the token's HMAC signature and required claims.
// Returns (ro, true) on success, (false, false) on failure.
func (a *Authenticator) verify(token, room string) (ro bool, ok bool) {
	payloadB64, sigB64, cut := strings.Cut(token, ".")
	if !cut || payloadB64 == "" || sigB64 == "" {
		return false, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return false, false
	}
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(payloadB64))
	expected := mac.Sum(nil)
	if subtle.ConstantTimeCompare(sig, expected) != 1 {
		return false, false
	}
	// Signature is valid; enforce claims.
	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return false, false
	}
	var claims struct {
		Exp  int64  `json:"exp"`
		Room string `json:"room"`
		RO   bool   `json:"ro"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return false, false
	}

	// exp is REQUIRED (mirrors Node's SECURE-mode policy). Tokens without exp
	// are permanently valid and must not be accepted — they ride in the URL
	// and may leak into logs, browser history, or caches.
	if claims.Exp == 0 {
		return false, false
	}
	now := time.Now().Unix()
	if now > claims.Exp {
		return false, false // expired
	}
	// Optional max-TTL bound: reject tokens whose lifetime is suspiciously long.
	if a.maxTTL > 0 && claims.Exp-now > a.maxTTL {
		return false, false
	}

	// room is REQUIRED. A room-less token would act as a master key and
	// authenticate the bearer for ANY room on this server.
	if claims.Room == "" {
		return false, false
	}
	if claims.Room != room {
		return false, false
	}

	return claims.RO, true
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
