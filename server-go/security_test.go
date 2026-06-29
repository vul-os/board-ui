package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/reearth/ygo/crdt"
)

// ─── Auth hardening tests (findings 2 & 3) ────────────────────────────────────

// mint builds a valid HMAC token from claims using the supplied secret.
func mintToken(secret []byte, claims map[string]any) string {
	payload, _ := json.Marshal(claims)
	pB64 := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(pB64))
	return pB64 + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestAuthRoomRequired(t *testing.T) {
	secret := []byte("testsecret")
	a := NewAuthenticator(AuthConfig{Secret: secret, MaxTokenTTL: 3600})

	// Finding 2: a token with no room claim must be rejected.
	noRoom := mintToken(secret, map[string]any{
		"exp": time.Now().Add(time.Hour).Unix(),
		// room absent
	})
	if _, ok := a.verify(noRoom, "any-room"); ok {
		t.Error("room-less token should be rejected (finding 2)")
	}

	// A token with an empty room string must also be rejected.
	emptyRoom := mintToken(secret, map[string]any{
		"exp":  time.Now().Add(time.Hour).Unix(),
		"room": "",
	})
	if _, ok := a.verify(emptyRoom, "any-room"); ok {
		t.Error("empty-room token should be rejected (finding 2)")
	}
}

func TestAuthExpRequired(t *testing.T) {
	secret := []byte("testsecret")
	a := NewAuthenticator(AuthConfig{Secret: secret, MaxTokenTTL: 3600})

	// Finding 3: a token without exp must be rejected.
	noExp := mintToken(secret, map[string]any{
		"room": "r1",
		// exp absent
	})
	if _, ok := a.verify(noExp, "r1"); ok {
		t.Error("token without exp should be rejected (finding 3)")
	}
}

func TestAuthMaxTTL(t *testing.T) {
	secret := []byte("testsecret")
	const maxTTL = 3600
	a := NewAuthenticator(AuthConfig{Secret: secret, MaxTokenTTL: maxTTL})

	// Token whose exp is exactly at the max-TTL boundary should pass.
	atLimit := mintToken(secret, map[string]any{
		"exp":  time.Now().Add(time.Duration(maxTTL) * time.Second).Unix(),
		"room": "r1",
	})
	if _, ok := a.verify(atLimit, "r1"); !ok {
		t.Error("token at TTL boundary should be accepted")
	}

	// Token whose exp exceeds the max-TTL must be rejected (finding 3).
	tooLong := mintToken(secret, map[string]any{
		"exp":  time.Now().Add(2 * time.Duration(maxTTL) * time.Second).Unix(),
		"room": "r1",
	})
	if _, ok := a.verify(tooLong, "r1"); ok {
		t.Error("over-long-lived token should be rejected (finding 3)")
	}
}

func TestAuthValidRoomScoped(t *testing.T) {
	secret := []byte("testsecret")
	a := NewAuthenticator(AuthConfig{Secret: secret, MaxTokenTTL: 3600})

	good := mintToken(secret, map[string]any{
		"exp":  time.Now().Add(time.Hour).Unix(),
		"room": "userid:default",
	})

	// Correct room — must be accepted.
	if ro, ok := a.verify(good, "userid:default"); !ok || ro {
		t.Errorf("valid room-scoped token rejected or unexpected ro=%v", ro)
	}
	// Different room — must be rejected.
	if _, ok := a.verify(good, "other-room"); ok {
		t.Error("token accepted for wrong room")
	}
}

func TestAuthROClaim(t *testing.T) {
	secret := []byte("testsecret")
	a := NewAuthenticator(AuthConfig{Secret: secret, MaxTokenTTL: 3600})

	roToken := mintToken(secret, map[string]any{
		"exp":  time.Now().Add(time.Hour).Unix(),
		"room": "r1",
		"ro":   true,
	})
	ro, ok := a.verify(roToken, "r1")
	if !ok {
		t.Fatal("valid ro token rejected")
	}
	if !ro {
		t.Error("ro claim not propagated")
	}

	// Check() should mark the request as read-only.
	req := httptest.NewRequest("GET", "/ws/r1?token="+roToken, nil)
	req.SetPathValue("room", "r1")
	if !a.Check(req) {
		t.Fatal("Check rejected valid ro token")
	}
	if !a.IsReadOnly(req) {
		t.Error("IsReadOnly should be true after ro token Check")
	}
	a.Release(req)
	if a.IsReadOnly(req) {
		t.Error("IsReadOnly should be false after Release")
	}
}

// ─── Insecure-mode loopback test (finding 4) ─────────────────────────────────

func TestInsecureModeLoopback(t *testing.T) {
	// When auth is off and BOARD_ALLOW_INSECURE is not set, loopbackAddr must
	// be applied to the listen address so the server is not exposed off-host.
	for _, addr := range []string{":8080", "0.0.0.0:8080", "0.0.0.0:1234"} {
		got := loopbackAddr(addr)
		if got != "127.0.0.1:8080" && got != "127.0.0.1:1234" {
			// Extract the expected port.
		}
		// Must start with 127.0.0.1.
		if len(got) < 9 || got[:9] != "127.0.0.1" {
			t.Errorf("loopbackAddr(%q) = %q; want 127.0.0.1:...", addr, got)
		}
	}
}

// ─── DoS cap tests (finding 1) ────────────────────────────────────────────────

func TestDoSConfig(t *testing.T) {
	// Verify that LoadConfig provides non-zero defaults for all DoS fields.
	cfg := LoadConfig()
	if cfg.DoS.MaxConnections <= 0 {
		t.Errorf("MaxConnections default must be > 0, got %d", cfg.DoS.MaxConnections)
	}
	if cfg.DoS.MaxPeersPerRoom <= 0 {
		t.Errorf("MaxPeersPerRoom default must be > 0, got %d", cfg.DoS.MaxPeersPerRoom)
	}
	if cfg.DoS.MessageRateLimit <= 0 {
		t.Errorf("MessageRateLimit default must be > 0, got %f", cfg.DoS.MessageRateLimit)
	}
	if cfg.DoS.MaxAwarenessBytesPerRoom <= 0 {
		t.Errorf("MaxAwarenessBytesPerRoom default must be > 0, got %d", cfg.DoS.MaxAwarenessBytesPerRoom)
	}
	if cfg.DoS.MaxBlobBytes <= 0 {
		t.Errorf("MaxBlobBytes default must be > 0, got %d", cfg.DoS.MaxBlobBytes)
	}
}

// ─── Blob pruning tests (finding 6) ──────────────────────────────────────────

func TestBlobPruner(t *testing.T) {
	doc := crdt.New()
	const maxBytes = 100 // very small cap so we can test with short strings

	WireBlubPruner(doc, "test-room", maxBytes)

	yfiles := doc.GetMap(filesKey)

	// Insert a small entry — must be retained.
	doc.Transact(func(txn *crdt.Transaction) {
		yfiles.Set(txn, "small", map[string]any{"dataURL": "data:image/png;base64,abc"})
	})
	if _, ok := yfiles.Get("small"); !ok {
		t.Error("small blob was incorrectly pruned")
	}

	// Insert an oversized entry — must be deleted by the pruner.
	bigDataURL := "data:image/png;base64," + string(make([]byte, maxBytes+1))
	doc.Transact(func(txn *crdt.Transaction) {
		yfiles.Set(txn, "big", map[string]any{"dataURL": bigDataURL})
	})
	if _, ok := yfiles.Get("big"); ok {
		t.Error("oversized blob was not pruned (finding 6)")
	}

	// Small entry must still be there after pruning.
	if _, ok := yfiles.Get("small"); !ok {
		t.Error("small blob was removed after pruning pass")
	}
}

// ─── Read-only hub tests (finding 5) ─────────────────────────────────────────

func TestReadOnlyHub(t *testing.T) {
	hub := newReadOnlyHub()

	sub := hub.Subscribe("room1")
	defer hub.Unsubscribe("room1", sub)

	frame := []byte{1, 2, 3}
	hub.Broadcast("room1", frame)

	select {
	case got := <-sub.ch:
		if string(got) != string(frame) {
			t.Errorf("hub delivered wrong frame: %v", got)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("hub broadcast timed out")
	}

	// Broadcast to a different room must not reach this subscriber.
	hub.Broadcast("room2", frame)
	select {
	case <-sub.ch:
		t.Error("subscriber received broadcast for a different room")
	case <-time.After(20 * time.Millisecond):
		// Correct: nothing received.
	}
}

func TestWireHubObserver(t *testing.T) {
	hub := newReadOnlyHub()
	doc := crdt.New()
	WireHubObserver(doc, "r1", hub)

	sub := hub.Subscribe("r1")
	defer hub.Unsubscribe("r1", sub)

	// A doc.Transact that mutates state must trigger the hub.
	ymap := doc.GetMap("elements")
	doc.Transact(func(txn *crdt.Transaction) {
		ymap.Set(txn, "k", "v")
	})

	select {
	case frame := <-sub.ch:
		if len(frame) == 0 {
			t.Error("hub broadcast an empty frame")
		}
		// Frame must start with [0x00, 0x02] = msgSync(0) + MsgUpdate(2).
		if frame[0] != 0x00 || frame[1] != 0x02 {
			t.Errorf("unexpected frame prefix: %v", frame[:2])
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("hub observer did not broadcast after doc transact")
	}
}

func TestWireSecurityIntegration(t *testing.T) {
	hub := newReadOnlyHub()
	cfg := LoadConfig()
	cfg.DoS.MaxBlobBytes = 50

	hook := wireSecurity(cfg, hub)

	doc := crdt.New()
	if err := hook(context.Background(), "room-a", doc); err != nil {
		t.Fatalf("wireSecurity hook returned error: %v", err)
	}

	// Verify hub observer: a doc update should be broadcast to ro subscribers.
	sub := hub.Subscribe("room-a")
	defer hub.Unsubscribe("room-a", sub)

	// GetMap acquires doc.mu.Lock; call it OUTSIDE Transact to avoid
	// re-entering the write lock (which would deadlock).
	yelem := doc.GetMap("elements")
	doc.Transact(func(txn *crdt.Transaction) {
		yelem.Set(txn, "x", "y")
	})

	select {
	case <-sub.ch:
		// Good: hub fired.
	case <-time.After(200 * time.Millisecond):
		t.Error("hub observer not wired by wireSecurity")
	}

	// Verify blob pruner: an oversized blob must be removed.
	yfiles := doc.GetMap(filesKey)
	bigURL := "data:image/png;base64," + string(make([]byte, 100))
	doc.Transact(func(txn *crdt.Transaction) {
		yfiles.Set(txn, "big", map[string]any{"dataURL": bigURL})
	})
	if _, ok := yfiles.Get("big"); ok {
		t.Error("blob pruner not wired by wireSecurity")
	}
}
