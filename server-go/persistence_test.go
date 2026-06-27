package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/reearth/ygo/crdt"
)

// memStore is an in-memory ObjectStore for tests.
type memStore struct {
	mu   sync.Mutex
	objs map[string][]byte
}

func newMemStore() *memStore { return &memStore{objs: map[string][]byte{}} }

func (m *memStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.objs[key]
	return v, ok, nil
}

func (m *memStore) Put(_ context.Context, key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.objs[key] = cp
	return nil
}

// fakeProvider serves a fixed doc per room.
type fakeProvider struct{ docs map[string]*crdt.Doc }

func (f *fakeProvider) GetDoc(room string) *crdt.Doc { return f.docs[room] }

// boardDoc builds a doc shaped like a board (elements + files YMaps) with one
// element so we can assert the opaque snapshot survives a storage round-trip.
func boardDoc(elementID string) *crdt.Doc {
	d := crdt.New()
	elements := d.GetMap("elements")
	files := d.GetMap("files")
	d.Transact(func(txn *crdt.Transaction) {
		elements.Set(txn, elementID, "rect")
		files.Set(txn, "f1", "blob")
	})
	return d
}

func TestBucketPersistenceRoundTrip(t *testing.T) {
	store := newMemStore()
	p := NewBucketPersistence(store, "board-app/", 600*time.Millisecond)

	const room = "userid:default"
	src := boardDoc("el-123")
	p.SetProvider(&fakeProvider{docs: map[string]*crdt.Doc{room: src}})

	// flush is the synchronous core of the debounced write-back.
	p.flush(room)

	// The snapshot must have landed at the documented bucket key.
	if _, ok := store.objs["board-app/board/userid_default.bin"]; !ok {
		t.Fatalf("snapshot not stored at expected key; have %v", keysOf(store))
	}

	// LoadDoc must return a V1 update that reconstructs the same state.
	update, err := p.LoadDoc(room)
	if err != nil {
		t.Fatalf("LoadDoc: %v", err)
	}
	if len(update) == 0 {
		t.Fatal("LoadDoc returned empty update for a stored room")
	}
	fresh := crdt.New()
	if err := crdt.ApplyUpdateV1(fresh, update, nil); err != nil {
		t.Fatalf("ApplyUpdateV1: %v", err)
	}
	if v, ok := fresh.GetMap("elements").Get("el-123"); !ok || v != "rect" {
		t.Fatalf("elements[el-123] = %v, %v; want rect, true", v, ok)
	}
	if v, ok := fresh.GetMap("files").Get("f1"); !ok || v != "blob" {
		t.Fatalf("files[f1] = %v, %v; want blob, true", v, ok)
	}
}

func TestBucketPersistenceMissingRoomEmpty(t *testing.T) {
	p := NewBucketPersistence(newMemStore(), "", 10*time.Millisecond)
	update, err := p.LoadDoc("never-seen")
	if err != nil {
		t.Fatalf("LoadDoc: %v", err)
	}
	if update != nil {
		t.Fatalf("absent room should seed empty (nil), got %d bytes", len(update))
	}
}

func TestDiskPersistenceRoundTrip(t *testing.T) {
	p := NewDiskPersistence(t.TempDir(), 600*time.Millisecond)
	const room = "channel-42"
	src := boardDoc("shape-1")
	p.SetProvider(&fakeProvider{docs: map[string]*crdt.Doc{room: src}})

	p.flush(room)

	update, err := p.LoadDoc(room)
	if err != nil || len(update) == 0 {
		t.Fatalf("LoadDoc disk: err=%v len=%d", err, len(update))
	}
	fresh := crdt.New()
	if err := crdt.ApplyUpdateV1(fresh, update, nil); err != nil {
		t.Fatalf("ApplyUpdateV1: %v", err)
	}
	if v, ok := fresh.GetMap("elements").Get("shape-1"); !ok || v != "rect" {
		t.Fatalf("disk round-trip lost state: %v, %v", v, ok)
	}
}

func TestStopFlushesPending(t *testing.T) {
	store := newMemStore()
	p := NewBucketPersistence(store, "", time.Hour) // long debounce so it won't fire on its own
	const room = "r1"
	p.SetProvider(&fakeProvider{docs: map[string]*crdt.Doc{room: boardDoc("x")}})

	if err := p.StoreUpdate(room, nil); err != nil {
		t.Fatal(err)
	}
	p.Stop() // must flush synchronously

	if _, ok := store.objs["board/r1.bin"]; !ok {
		t.Fatalf("Stop did not flush pending snapshot; have %v", keysOf(store))
	}
}

func TestSafeRoom(t *testing.T) {
	cases := map[string]string{
		"userid:default": "userid_default",
		"a/b":            "a_b",
		"../escape":      ".._escape", // dots kept but slash neutralised -> no traversal
		"plain-room.1":   "plain-room.1",
		"":               "",
		".":              "",
		"..":             "",
	}
	for in, want := range cases {
		if got := safeRoom(in); got != want {
			t.Errorf("safeRoom(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseEndpoint(t *testing.T) {
	ok := []struct {
		raw    string
		host   string
		secure bool
	}{
		{"https://s3.example.com", "s3.example.com", true},
		{"http://localhost:9000", "localhost:9000", false},
		{"http://127.0.0.1:9000", "127.0.0.1:9000", false},
		{"http://10.0.0.5:9000", "10.0.0.5:9000", false},
		{"minio:9000", "minio:9000", false}, // single-label docker service
		{"https://board.vulos.org", "board.vulos.org", true},
	}
	for _, c := range ok {
		host, secure, err := parseEndpoint(c.raw)
		if err != nil || host != c.host || secure != c.secure {
			t.Errorf("parseEndpoint(%q) = (%q,%v,%v); want (%q,%v,nil)", c.raw, host, secure, err, c.host, c.secure)
		}
	}
	// Plaintext to a public host must be rejected.
	for _, bad := range []string{"http://s3.amazonaws.com", "http://board.vulos.org:9000"} {
		if _, _, err := parseEndpoint(bad); err == nil {
			t.Errorf("parseEndpoint(%q) should reject plaintext public endpoint", bad)
		}
	}
}

func TestAuthenticator(t *testing.T) {
	secret := []byte("topsecret")
	a := NewAuthenticator(AuthConfig{Secret: secret})
	if !a.Enabled() {
		t.Fatal("authenticator should be enabled with a secret")
	}

	mint := func(claims map[string]any) string {
		payload, _ := json.Marshal(claims)
		pB64 := base64.RawURLEncoding.EncodeToString(payload)
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(pB64))
		return pB64 + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}

	// Valid, room-scoped, unexpired.
	good := mint(map[string]any{"room": "userid:default", "exp": time.Now().Add(time.Hour).Unix()})
	if !a.verify(good, "userid:default") {
		t.Error("valid token rejected")
	}
	// Wrong room.
	if a.verify(good, "other") {
		t.Error("token accepted for wrong room")
	}
	// Expired.
	expired := mint(map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})
	if a.verify(expired, "any") {
		t.Error("expired token accepted")
	}
	// Tampered signature.
	if a.verify(good[:len(good)-2]+"xy", "userid:default") {
		t.Error("tampered token accepted")
	}
	// No-claims token (signature only) is valid for any room.
	if !a.verify(mint(map[string]any{}), "whatever") {
		t.Error("unscoped valid token rejected")
	}

	// Check() via a request carrying ?token=.
	r := httptest.NewRequest("GET", "/ws/userid:default?token="+good, nil)
	r.SetPathValue("room", "userid:default")
	if !a.Check(r) {
		t.Error("Check rejected a valid request")
	}
	r2 := httptest.NewRequest("GET", "/ws/userid:default", nil)
	if a.Check(r2) {
		t.Error("Check accepted a request with no token")
	}

	// Disabled authenticator allows everything.
	open := NewAuthenticator(AuthConfig{})
	if open.Enabled() || !open.Check(httptest.NewRequest("GET", "/ws/x", nil)) {
		t.Error("disabled authenticator should allow all")
	}
}

func keysOf(m *memStore) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ks := make([]string, 0, len(m.objs))
	for k := range m.objs {
		ks = append(ks, k)
	}
	return ks
}
