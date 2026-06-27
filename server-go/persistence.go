// Package main is the production Vulos collaborative-board sync server. It
// mirrors wede's server-side Yjs approach (github.com/reearth/ygo — pure-Go,
// cgo-free, Yjs-v13 wire-compatible CRDT): the ygo provider/websocket Server
// speaks the y-websocket protocol (sync step1/2, update, awareness) that
// board-ui's createWebsocketProvider expects, and a PersistenceAdapter keeps
// each room's opaque CRDT snapshot in durable storage.
//
// The server never parses Excalidraw. A board's content lives in the doc's
// "elements" and "files" YMaps, but to this server it is an opaque Yjs snapshot:
// persistence is the raw crdt.EncodeStateAsUpdateV1(doc, nil) bytes per room.
package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/reearth/ygo/crdt"
)

// DocProvider is the subset of ygo's provider/websocket Server the persistence
// layer needs: fetch the live doc for a room so its full snapshot can be
// encoded. Defined as an interface so this package stays decoupled from ygo's
// concrete Server (and so tests can supply a fake).
type DocProvider interface {
	GetDoc(name string) *crdt.Doc
}

// snapshotSink is the durable backend behind the debounced adapter: load and
// replace a single room's opaque snapshot bytes. diskSink and bucketSink
// implement it.
type snapshotSink interface {
	// load returns the stored snapshot for room, or (nil, nil) when absent.
	load(room string) ([]byte, error)
	// save atomically replaces room's stored snapshot.
	save(room string, snapshot []byte) error
	// describe is a short human label for startup logging.
	describe() string
}

// Adapter is a ygo PersistenceAdapter that debounces writes and persists each
// room's full CRDT snapshot via a snapshotSink. It is the shared core of both
// the bucket and disk persistence modes; mirrors wede's collabdoc
// DiskPersistence (debounced StoreUpdate -> GetDoc -> encode -> write) but
// stores the raw snapshot bytes rather than materialized text.
type Adapter struct {
	sink     snapshotSink
	debounce time.Duration

	mu       sync.Mutex
	provider DocProvider
	timers   map[string]*time.Timer
	stopped  bool
}

// newAdapter wires a sink into a debounced PersistenceAdapter.
func newAdapter(sink snapshotSink, debounce time.Duration) *Adapter {
	return &Adapter{
		sink:     sink,
		debounce: debounce,
		timers:   make(map[string]*time.Timer),
	}
}

// NewDiskPersistence returns the local-dev fallback adapter, storing each room's
// snapshot as <root>/board/<safeRoom>.bin.
func NewDiskPersistence(root string, debounce time.Duration) *Adapter {
	return newAdapter(&diskSink{root: root}, debounce)
}

// NewBucketPersistence returns the production adapter, storing each room's
// snapshot as <prefix>board/<safeRoom>.bin in the object store.
func NewBucketPersistence(store ObjectStore, prefix string, debounce time.Duration) *Adapter {
	return newAdapter(&bucketSink{store: store, prefix: prefix}, debounce)
}

// Describe returns the sink's startup label.
func (a *Adapter) Describe() string { return a.sink.describe() }

// SetProvider wires the live document source (the ygo Server). Until set,
// StoreUpdate is a no-op.
func (a *Adapter) SetProvider(p DocProvider) {
	a.mu.Lock()
	a.provider = p
	a.mu.Unlock()
}

// LoadDoc implements ygo's PersistenceAdapter: it returns the stored snapshot as
// a V1 update for the provider to seed the room, or (nil, nil) for a new/absent
// room. The stored bytes are themselves a V1 update; we round-trip them through
// a fresh doc to validate (a corrupt snapshot starts the room empty rather than
// failing the connection).
func (a *Adapter) LoadDoc(room string) ([]byte, error) {
	raw, err := a.sink.load(room)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	doc := crdt.New()
	if err := crdt.ApplyUpdateV1(doc, raw, nil); err != nil {
		log.Printf("[board] WARNING: corrupt snapshot for room %q (%v); starting empty", room, err)
		return nil, nil
	}
	return crdt.EncodeStateAsUpdateV1(doc, nil), nil
}

// StoreUpdate implements ygo's PersistenceAdapter: fired on every committed
// update, it schedules a debounced full-snapshot write so we coalesce bursts of
// edits into one storage PUT.
func (a *Adapter) StoreUpdate(room string, _ []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped || a.provider == nil {
		return nil
	}
	if t, ok := a.timers[room]; ok {
		t.Reset(a.debounce)
		return nil
	}
	a.timers[room] = time.AfterFunc(a.debounce, func() { a.flush(room) })
	return nil
}

// flush encodes the room's current full snapshot and persists it.
func (a *Adapter) flush(room string) {
	a.mu.Lock()
	delete(a.timers, room)
	prov, stopped := a.provider, a.stopped
	a.mu.Unlock()
	if prov == nil || stopped {
		return
	}
	doc := prov.GetDoc(room)
	if doc == nil {
		return
	}
	snapshot := crdt.EncodeStateAsUpdateV1(doc, nil)
	if err := a.sink.save(room, snapshot); err != nil {
		log.Printf("[board] WARNING: failed to persist room %q: %v", room, err)
	}
}

// Stop cancels pending timers and performs a final synchronous flush so the last
// edits are persisted. Called on shutdown.
func (a *Adapter) Stop() {
	a.mu.Lock()
	a.stopped = true
	rooms := make([]string, 0, len(a.timers))
	for r, t := range a.timers {
		t.Stop()
		rooms = append(rooms, r)
	}
	a.timers = make(map[string]*time.Timer)
	prov := a.provider
	a.mu.Unlock()

	if prov == nil {
		return
	}
	for _, room := range rooms {
		doc := prov.GetDoc(room)
		if doc == nil {
			continue
		}
		if err := a.sink.save(room, crdt.EncodeStateAsUpdateV1(doc, nil)); err != nil {
			log.Printf("[board] WARNING: final flush failed for room %q: %v", room, err)
		}
	}
}

// unsafeRoomChar matches any character not allowed in a storage key/file segment.
var unsafeRoomChar = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

// safeRoom maps a y-websocket room name to a safe single-segment storage key.
//
// Board room ids are typically "<userid>:default" or a channel id. The
// y-websocket client does NOT URL-encode the room, so the server receives it
// verbatim as the trailing path segment (e.g. "userid:default"); ':' is a legal
// pchar so the segment arrives intact. For the storage key we replace any
// character outside [A-Za-z0-9._-] with '_' (matching the Node dev server's
// roomFile sanitization, so disk data is interchangeable between the two), which
// also forecloses path traversal. Empty/"."/".." resolve to "" => the caller
// treats the room as un-storable (new empty doc, no write).
func safeRoom(room string) string {
	s := unsafeRoomChar.ReplaceAllString(room, "_")
	if s == "" || s == "." || s == ".." {
		return ""
	}
	return s
}

// diskSink stores snapshots on the local filesystem (local-dev fallback).
type diskSink struct{ root string }

func (d *diskSink) describe() string { return "disk:" + d.root }

func (d *diskSink) path(room string) (string, bool) {
	safe := safeRoom(room)
	if safe == "" {
		return "", false
	}
	return filepath.Join(d.root, "board", safe+".bin"), true
}

func (d *diskSink) load(room string) ([]byte, error) {
	full, ok := d.path(room)
	if !ok {
		return nil, nil
	}
	data, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

func (d *diskSink) save(room string, snapshot []byte) error {
	full, ok := d.path(room)
	if !ok {
		return nil
	}
	return writeAtomic(full, snapshot)
}

// writeAtomic writes data to path via a temp file + rename, creating parent dirs.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".board-snap-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// bucketSink stores snapshots in the Vulos storage bucket under
// <prefix>board/<safeRoom>.bin.
type bucketSink struct {
	store  ObjectStore
	prefix string // already normalised to a trailing slash (or empty)
}

func (b *bucketSink) describe() string { return "bucket:" + b.prefix + "board/" }

func (b *bucketSink) key(room string) (string, bool) {
	safe := safeRoom(room)
	if safe == "" {
		return "", false
	}
	return b.prefix + "board/" + safe + ".bin", true
}

func (b *bucketSink) load(room string) ([]byte, error) {
	key, ok := b.key(room)
	if !ok {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data, found, err := b.store.Get(ctx, key)
	if err != nil || !found {
		return nil, err
	}
	return data, nil
}

func (b *bucketSink) save(room string, snapshot []byte) error {
	key, ok := b.key(room)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return b.store.Put(ctx, key, snapshot)
}
