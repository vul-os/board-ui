package main

import (
	"context"
	"log"
	"net/http"
	"sync"

	gorillaws "github.com/gorilla/websocket"
	"github.com/reearth/ygo/crdt"
	"github.com/reearth/ygo/encoding"
)

// wireSecurity returns an OnLoadDocument hook that wires both blob pruning
// (finding 6) and the ro hub observer (finding 5) for every room.
func wireSecurity(cfg Config, hub *ReadOnlyHub) func(context.Context, string, *crdt.Doc) error {
	return func(_ context.Context, room string, doc *crdt.Doc) error {
		// Blob pruning: delete oversized dataURLs from the files YMap.
		WireBlubPruner(doc, room, cfg.DoS.MaxBlobBytes)
		// Ro hub: broadcast every doc update to all ro subscribers of this room.
		WireHubObserver(doc, room, hub)
		return nil
	}
}

// ─── Blob pruning ────────────────────────────────────────────────────────────

// filesKey is the YMap name that stores Excalidraw file blobs. Must match
// src/doc.ts FILES_KEY and the Node server's FILES_KEY constant.
const filesKey = "files"

// WireBlubPruner attaches a YMap observer to doc's "files" map that deletes
// any entry whose value — after JSON-unmarshalling — contains a "dataURL" string
// exceeding maxBytes. The deletion replicates to all peers.
//
// This is the Go port of the Node blob-pruning logic (index.mjs:177-195).
// The observer fires synchronously after each transaction that touches the
// files map; oversized entries are deleted in a new transaction so the observer
// does not run recursively (the deleted entry no longer exists on re-fire).
func WireBlubPruner(doc *crdt.Doc, room string, maxBytes int64) {
	yfiles := doc.GetMap(filesKey)
	yfiles.Observe(func(e crdt.YMapEvent) {
		var oversized []string
		for key := range e.KeysChanged {
			val, ok := yfiles.Get(key)
			if !ok {
				continue
			}
			dataURL := extractDataURL(val)
			if int64(len(dataURL)) > maxBytes {
				oversized = append(oversized, key)
			}
		}
		if len(oversized) == 0 {
			return
		}
		doc.Transact(func(txn *crdt.Transaction) {
			for _, k := range oversized {
				yfiles.Delete(txn, k)
			}
		})
		log.Printf("[board] dropped %d oversized blob(s) (> %d bytes) in room %q",
			len(oversized), maxBytes, room)
	})
}

// extractDataURL pulls the "dataURL" string out of a file entry value. File
// entries stored by Excalidraw are JSON objects like
// {"dataURL":"data:image/png;base64,...","mimeType":"image/png",...}.
// In ygo these are stored as map[string]any (ContentAny/ContentJSON).
func extractDataURL(val any) string {
	switch v := val.(type) {
	case map[string]any:
		if s, ok := v["dataURL"].(string); ok {
			return s
		}
	case string:
		// Unusual: file stored as a bare string — treat the whole value as potential dataURL.
		return v
	}
	return ""
}

// ─── Read-only hub ────────────────────────────────────────────────────────────

// ReadOnlyHub fans out live Y.Doc updates to read-only WebSocket peers.
// When a ro peer connects, it subscribes to the hub for its room; each time
// any peer commits an update to that room the hub broadcasts the binary wire
// frame (already encoded for the y-websocket protocol) to all ro subscribers.
type ReadOnlyHub struct {
	mu   sync.Mutex
	subs map[string]map[*roSub]struct{} // room → set of subscribers
}

// roSub is one read-only peer's subscription.
type roSub struct {
	ch chan []byte // buffered; drop on overflow to avoid slow-reader blocking hub
}

// newReadOnlyHub returns an empty hub.
func newReadOnlyHub() *ReadOnlyHub {
	return &ReadOnlyHub{subs: make(map[string]map[*roSub]struct{})}
}

// Subscribe adds a subscriber for room and returns it. Call Unsubscribe when
// the peer disconnects.
func (h *ReadOnlyHub) Subscribe(room string) *roSub {
	sub := &roSub{ch: make(chan []byte, 256)}
	h.mu.Lock()
	if h.subs[room] == nil {
		h.subs[room] = make(map[*roSub]struct{})
	}
	h.subs[room][sub] = struct{}{}
	h.mu.Unlock()
	return sub
}

// Unsubscribe removes the subscriber. Safe to call more than once.
func (h *ReadOnlyHub) Unsubscribe(room string, sub *roSub) {
	h.mu.Lock()
	if set, ok := h.subs[room]; ok {
		delete(set, sub)
		if len(set) == 0 {
			delete(h.subs, room)
		}
	}
	h.mu.Unlock()
}

// Broadcast sends a wire frame to all current subscribers of room.
func (h *ReadOnlyHub) Broadcast(room string, frame []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs[room] {
		select {
		case sub.ch <- frame:
		default:
			// Slow ro peer: drop rather than block the hub.
		}
	}
}

// WireHubObserver registers a doc.OnUpdate listener that encodes each update as
// a y-websocket MsgUpdate wire frame and broadcasts it to all ro subscribers of
// room. Call this inside OnLoadDocument so every room gets the observer.
func WireHubObserver(doc *crdt.Doc, room string, hub *ReadOnlyHub) {
	doc.OnUpdate(func(update []byte, _ any) {
		frame := encodeMsgUpdate(update)
		hub.Broadcast(room, frame)
	})
}

// ─── Read-only WebSocket handler ─────────────────────────────────────────────

// roUpgrader is the gorilla upgrader used for read-only connections. Same
// permissive CheckOrigin as ygo (its own origin check is applied before we
// reach here). No subprotocol negotiation required.
var roUpgrader = gorillaws.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true }, // ygo already checked origin
}

// ServeReadOnly upgrades r to a WebSocket, sends the current doc snapshot (sync
// step 2), then relays live updates from hub. Client → server sync-update
// messages are silently dropped (enforcing the ro claim). Awareness messages
// from the client are allowed through to preserve presence (not relayed to room
// peers since we operate outside ygo's peer lifecycle).
//
// Parameters:
//   - w, r: the HTTP upgrade request (already auth-checked by the Authenticator).
//   - room: the y-websocket room name extracted from the path.
//   - adapter: persistence adapter used to load the initial snapshot.
//   - hub: the ReadOnlyHub to subscribe to for live updates.
func ServeReadOnly(w http.ResponseWriter, r *http.Request, room string, adapter *Adapter, hub *ReadOnlyHub) {
	ws, err := roUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return // gorilla already wrote the error response
	}
	defer ws.Close()

	// Subscribe BEFORE loading the initial state so we don't miss updates that
	// arrive between LoadDoc and the goroutine starting.
	sub := hub.Subscribe(room)
	defer hub.Unsubscribe(room, sub)

	// Load current snapshot. For a room with active peers, adapter.LoadDoc
	// returns the last flushed snapshot; live updates that haven't been flushed
	// yet will arrive via the hub immediately after (CRDTs are idempotent, so
	// the client handles any overlap).
	initialState, err := adapter.LoadDoc(room)
	if err != nil {
		log.Printf("[board] ro: LoadDoc for room %q: %v", room, err)
		return
	}

	// Send sync step 2 — the full current state. An empty state means the room
	// is new; still send a valid empty-update so the client knows it's in sync.
	if len(initialState) == 0 {
		// Empty V1 update: just the header bytes for an empty state.
		initialState = crdt.EncodeStateAsUpdateV1(crdt.New(), nil)
	}
	if err := ws.WriteMessage(gorillaws.BinaryMessage, encodeSyncStep2(initialState)); err != nil {
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Writer goroutine: forward hub updates to the ro client.
	go func() {
		defer cancel()
		for {
			select {
			case frame, ok := <-sub.ch:
				if !ok {
					return
				}
				if err := ws.WriteMessage(gorillaws.BinaryMessage, frame); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Read loop: drop sync-update messages, allow other message types.
	ws.SetReadLimit(16 << 20) // 16 MiB — matches Node's MAX_MESSAGE_BYTES default
	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		if mt != gorillaws.BinaryMessage || len(data) == 0 {
			continue
		}
		// y-websocket outer message type is a VarUint at byte 0.
		// msgSync (0): contains sync sub-messages. Sub-type at byte 1.
		//   sub-type 2 = MsgUpdate — this is a write; DROP it.
		//   sub-type 0 = SyncStep1 — client requests our state; ignore for ro.
		// msgAwareness (1): presence updates — allow but we can't relay without
		// being a ygo peer; harmlessly consumed to keep the connection alive.
		// All other types: consume silently.
		if data[0] == 0 && len(data) > 1 && data[1] == 2 {
			// Sync update (msgSync=0, MsgUpdate=2): read-only gate — drop.
			log.Printf("[board] ro: dropped write attempt in room %q", room)
		}
		// Everything else is consumed (no relay — ro peers don't write to the doc).
	}
}

// ─── Wire encoding helpers ────────────────────────────────────────────────────

// encodeSyncStep2 builds a y-websocket sync step-2 wire frame:
//
//	[varuint:0 (msgSync)] [varuint:1 (MsgSyncStep2)] [varbytes:update]
func encodeSyncStep2(update []byte) []byte {
	enc := encoding.NewEncoder()
	enc.WriteVarUint(0) // msgSync
	enc.WriteVarUint(1) // MsgSyncStep2
	enc.WriteVarBytes(update)
	return enc.Bytes()
}

// encodeMsgUpdate builds a y-websocket update broadcast wire frame:
//
//	[varuint:0 (msgSync)] [varuint:2 (MsgUpdate)] [varbytes:update]
//
// This is the same encoding ygo uses in encodeBroadcastWire.
func encodeMsgUpdate(update []byte) []byte {
	enc := encoding.NewEncoder()
	enc.WriteVarUint(0) // msgSync
	enc.WriteVarUint(2) // MsgUpdate
	enc.WriteVarBytes(update)
	return enc.Bytes()
}
