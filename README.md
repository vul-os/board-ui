# @vulos/board-ui

<sub>Part of <strong><a href="https://vulos.org">VulOS</a></strong> — the open, self-hostable web OS &amp; app suite. Runs standalone, or combined under one login by <a href="https://vulos.org">Vulos Workspace</a>.</sub>

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE)
&nbsp;TypeScript · React · Yjs

A Vulos-native collaborative whiteboard, delivered as a reusable React
component. Think *"an Excalidraw, but Vulos-native and Yjs-based"* so the same
board embeds into multiple host apps without forking.

## What is @vulos/board-ui?

`@vulos/board-ui` wraps the MIT [Excalidraw](https://github.com/excalidraw/excalidraw)
editor around a **Yjs CRDT core**. The board document *is* a `Y.Doc`. This
library owns the editor, the Yjs ⇄ Excalidraw binding (per-element CRDT merge),
cursor/presence rendering and snapshot encode/decode.

The crucial seam: **the host owns the transport and persistence; board-ui owns
the editor.** The host creates the `Y.Doc`, connects a provider (websocket /
LiveKit data channel / relay) and persists snapshots. board-ui just binds
Excalidraw to whatever `Y.Doc` it is handed. That is what lets one component
embed into Workspace, Meet and Talk unchanged.

It depends on the real `@excalidraw/excalidraw` npm package (not vendored) plus
`yjs`, `y-websocket` and `y-protocols`. There is no dependency on any other
Vulos repo — the only coupling is the `Y.Doc` contract.

## Part of VulOS

[VulOS](https://vulos.org) is an open, self-hostable web OS + app suite. Each
product is self-hostable on its own and can be combined under one login by
**Vulos Workspace**:

- **Vulos Mail** — mail + calendar + contacts (engine: lilmail; UI: `@vulos/mail-ui`; server: vulos-mail)
- **Vulos Talk** — team chat + channels/Spaces + huddles
- **Vulos Meet** — video meetings (LiveKit SFU)
- **Vulos Office** — documents: docs, sheets, slides, PDF
- **Vulos Relay** — sovereign connectivity fabric (`@vulos/relay-client`)
- **Vulos Workspace** — the open suite shell (one login, app switcher, admin)
- **Vulos OS** — the web-native desktop

`@vulos/board-ui` is the **shared whiteboard surface** mounted by Talk huddles,
Meet sessions and Workspace boards. It renders against a `Y.Doc` only, so it
runs standalone (against the bundled board sync server) **and** is combined by
Vulos Workspace. Products never import one another's code.

## Features

- **Yjs CRDT core** — elements stored one-per-id in a `Y.Map`, so concurrent
  edits to different shapes merge cleanly instead of last-write-wins.
- **Excalidraw editor** — the full MIT editor, wrapped not forked.
- **Transport-agnostic** — host supplies the `Y.Doc` + provider; works over
  websocket, LiveKit data channel or Vulos Relay with no code change.
- **Live presence** — remote cursors, names and selections via an optional
  `y-protocols` Awareness. Omit it and the board still works solo.
- **Snapshot helpers** — `encodeSnapshot` / `applySnapshot` for uniform
  persistence on hosts and servers.
- **Bundled sync server** — a minimal `y-websocket` server with snapshot
  persistence so Workspace and local dev have a working backend.
- **Read-only mode** — `readOnly` maps to Excalidraw `viewModeEnabled`.

## Quick start (standalone)

```bash
npm install

# Terminal 1 — the board sync server (persists to ./.board-data/<room>.bin)
npm run server

# Terminal 2 — the standalone demo host
npm run dev          # open http://localhost:5174
```

Open the demo in two browser tabs and draw — elements and cursors sync live.
This demo *is* the Workspace integration mode in miniature
(`createWebsocketProvider` → the bundled server).

Want it with **no backend at all**? `npm run dev` then open
`http://localhost:5174/local.html` — the `examples/local-doc` host wires a plain
local `Y.Doc` straight into `<BoardApp/>` (no server, no provider, solo).

## How it works

```
        HOST APP (owns transport + persistence)
        ┌───────────────────────────────────────────┐
        │  Y.Doc  ◀──▶  provider  ◀──▶  network       │
        └───────┬─────────────────────────▲──────────┘
                │ ydoc + awareness         │ snapshots
                ▼                          │
        ┌───────────────────────────────────────────┐
        │  @vulos/board-ui  <BoardApp/>               │
        │  ┌─────────────────────────────────────┐   │
        │  │ ExcalidrawYBinding                  │   │
        │  │   Y.Map<id, element>  ⇄  Excalidraw │   │
        │  │   Y.Map<id, file>     (image blobs) │   │
        │  └─────────────────────────────────────┘   │
        │  presence: Awareness ⇄ collaborators        │
        └───────────────────────────────────────────┘
```

`onChange` diffs the scene against the `Y.Map` and writes only changed/added/
deleted elements inside one `ydoc.transact(…, origin)`. `observeDeep` rebuilds
the scene on remote changes and calls `updateScene` — guarded by an origin
check and an "applying remote" flag so a change never echoes back to Yjs.

## Public API

```ts
import {
  BoardApp,
  createBoardDoc,
  encodeSnapshot,
  applySnapshot,
  ELEMENTS_KEY,
  FILES_KEY,
  createWebsocketProvider,
  type BoardUser,
} from '@vulos/board-ui'
import '@vulos/board-ui/style.css'   // once, in the host app

export interface BoardUser { id: string; name: string; color?: string }

// The main component. Host passes in a Y.Doc + (optional) awareness;
// board-ui binds Excalidraw to it.
function BoardApp(props: {
  ydoc: import('yjs').Doc
  awareness?: import('y-protocols/awareness').Awareness  // presence/cursors; optional
  user: BoardUser
  boardId: string
  readOnly?: boolean
}): JSX.Element

// Yjs doc helpers (so hosts/servers create/snapshot docs uniformly):
function createBoardDoc(): import('yjs').Doc
function encodeSnapshot(doc: import('yjs').Doc): Uint8Array        // Y.encodeStateAsUpdate
function applySnapshot(doc: import('yjs').Doc, bytes: Uint8Array): void // Y.applyUpdate
const ELEMENTS_KEY: string  // the Y.Map<string, ExcalidrawElement> key holding the scene
const FILES_KEY: string     // the Y.Map<string, BinaryFileData> key holding image blobs

// Convenience transport for standalone hosts (Workspace). Hosts with their own
// transport (Meet/Talk) do NOT use this.
function createWebsocketProvider(opts: {
  url: string; room: string; doc: import('yjs').Doc; token?: string
}): import('y-websocket').WebsocketProvider
```

Advanced (for hosts building a custom shell): `ExcalidrawYBinding`,
`bindAwareness` and their types are also exported.

## Embedding

board-ui never owns the transport. A host supplies a `Y.Doc` (and optional
Awareness) and keeps it in sync however it likes. Three host modes:

### 1. Workspace — bundled websocket server

```ts
const ydoc = createBoardDoc()
const provider = createWebsocketProvider({ url: 'wss://boards.example', room: boardId, doc: ydoc, token })
<BoardApp ydoc={ydoc} awareness={provider.awareness} user={me} boardId={boardId} />
```

In **production** the board is served by the Go sync server in `server-go/`
(see [Board sync server (Go)](#board-sync-server-go) below), which persists each
room's opaque Yjs snapshot to the Vulos storage bucket under a `board/<room>.bin`
key. The Node `npm run server` (`server/index.mjs`) is the **dev-only fallback**
— same y-websocket wire protocol, disk snapshots — for local hacking.

### 2. Meet — LiveKit data channel

Meet already has a realtime mesh, so it implements its own `Y.Doc` provider
that pumps Yjs updates over the LiveKit data channel:

```ts
const ydoc = createBoardDoc()
const awareness = new Awareness(ydoc)

// outbound: ship every local update
ydoc.on('update', (update, origin) => {
  if (origin === 'remote') return
  room.localParticipant.publishData(update, { reliable: true, topic: 'board' })
})
// inbound: apply peers' updates
room.on(RoomEvent.DataReceived, (payload, _p, _k, topic) => {
  if (topic === 'board') applySnapshot(ydoc, payload)   // Y.applyUpdate under the hood
})

<BoardApp ydoc={ydoc} awareness={awareness} user={me} boardId={sessionId} />
```

Late joiners get the current board by requesting a `encodeSnapshot(ydoc)` from
an existing participant (or the room metadata) and `applySnapshot`-ing it.

### 3. Talk — Vulos Relay

Talk huddles run over `@vulos/relay-client`. The host implements a provider
that sends/receives Yjs updates over the relay channel, exactly like Meet but
on relay transport, and persists each channel's `encodeSnapshot` to the storage
bucket per channel.

```ts
const ydoc = createBoardDoc()
relay.on('board-update', (bytes) => applySnapshot(ydoc, bytes))
ydoc.on('update', (update, origin) => { if (origin !== 'remote') relay.send('board-update', update) })
<BoardApp ydoc={ydoc} awareness={awareness} user={me} boardId={channelId} />
```

**Adapter authors:** everything you need is `encodeSnapshot(doc)` (= full
state, for late-join / persistence), `applySnapshot(doc, bytes)` (= apply any
update, tag incoming ones with a `'remote'` transaction origin so they are not
re-broadcast), and `doc.on('update', …)` for outbound diffs. Awareness is an
ordinary `y-protocols` Awareness — encode/apply its updates with
`y-protocols/awareness` `encodeAwarenessUpdate`/`applyAwarenessUpdate` over your
channel, or just pass a provider's `.awareness`.

## Board sync server (Go)

`server-go/` is the **production** board sync backend: a single CGO-free static
Go binary that mirrors how [wede](https://github.com/) does server-side Yjs. It
embeds [`github.com/reearth/ygo`](https://github.com/reearth/ygo) — a pure-Go,
cgo-free, Yjs-v13 wire-compatible CRDT — and its `provider/websocket.Server`
speaks the exact y-websocket protocol (sync step1/2, update, awareness) that
`createWebsocketProvider` (a standard `y-websocket` `WebsocketProvider`) uses. It
is a separate Go module (`github.com/vul-os/board-ui/server-go`) and is **not**
part of the npm package (excluded from `files`/`tsconfig`/`vite`).

It never parses Excalidraw: a board's content is `doc.getMap('elements')` +
`doc.getMap('files')`, but to the server it is an opaque snapshot. Persistence is
the raw `crdt.EncodeStateAsUpdateV1(doc, nil)` bytes per room.

**Endpoint & rooms.** The server serves the y-websocket endpoint at `/ws/{room}`.
The `y-websocket` client connects to `${url}/${room}`, so the host default
`VITE_BOARD_WS_URL = wss://board.vulos.org/ws` resolves to
`wss://board.vulos.org/ws/<room>?token=…`. The room is the board id
(e.g. `userid:default` or a channel id) and arrives verbatim as the trailing
path segment (`:` is a legal path char); ygo reads it from the `{room}` path
value. **No host change is needed** for that default — just terminate TLS and
proxy `/ws/` to this server. For the storage key the room is sanitised to
`[A-Za-z0-9._-]` (anything else → `_`), matching the Node dev server.

**Persistence.** Bucket layout `<VULOS_STORAGE_PREFIX>board/<room>.bin`; the
snapshot format is `EncodeStateAsUpdateV1`. Writes are debounced ~600ms (like
wede) then one full-snapshot `PutObject` (atomic per key). The S3/MinIO client is
`minio-go/v7` (matching vulos-mail's `internal/blob` convention). When the
storage seam env is unset, a **disk fallback** writes `<BOARD_DATA_DIR>/board/<room>.bin`
(temp-file + rename) for local dev. The active mode is logged at startup. A
plaintext (non-https) external endpoint is refused unless it is loopback/private.

**Auth (seam A).** If `BOARD_AUTH_SECRET` is set, the websocket upgrade requires
a valid `?token=`, verified with HMAC-SHA256 + `crypto/subtle.ConstantTimeCompare`.
Token shape (mintable by the OS gateway / CP, the intended minter):

```
token = base64url(payloadJSON) + "." + base64url(HMAC_SHA256(secret, base64url(payloadJSON)))
payloadJSON = {"exp": <unix-seconds, optional>, "room": "<board id, optional>"}
```

`exp` (if present) is enforced; `room` (if non-empty) must equal the joined room.
If `BOARD_AUTH_SECRET` is unset the server runs open and logs a warning (dev).

```bash
cd server-go
go build .            # CGO_ENABLED=0 works — proves pure-Go
./server-go           # disk mode on :8080, auth off (dev)
go test ./...         # snapshot round-trip + auth tests
docker build -t vulos-board-server .
```

## Configuration

Production Go sync server (`server-go/`) environment:

| Var | Default | Purpose |
| --- | --- | --- |
| `BOARD_LISTEN_ADDR` | `:8080` | HTTP listen address (serves `/ws/{room}`, `/healthz`) |
| `BOARD_DATA_DIR` | `./.board-data` | disk-fallback snapshot dir (when no storage seam) |
| `BOARD_AUTH_SECRET` | — | HMAC token secret; unset ⇒ auth disabled (dev) |
| `BOARD_ALLOWED_ORIGINS` | — | CORS allow-list for the WS upgrade (space/comma sep); empty ⇒ same-origin |
| `VULOS_STORAGE_ENDPOINT` | — | object-store URL (`https://…` or loopback/private `http://`); empty ⇒ disk |
| `VULOS_STORAGE_BUCKET` | — | bucket name (required for bucket mode) |
| `VULOS_STORAGE_PREFIX` | — | key prefix (e.g. `board-app/`) |
| `VULOS_STORAGE_REGION` | `us-east-1` | S3 region |
| `VULOS_STORAGE_ACCESS_KEY` / `_SECRET_KEY` / `_SESSION_TOKEN` | — | credentials (STS token optional) |

It is designed to sit behind the OS gateway proxy at `/app/board/` like the
other products.

Node dev-fallback server (`npm run server`, `server/index.mjs`) environment. This
server is **dev-only** and is **not** shipped in the npm package (excluded from
`files`); production uses the Go server above.

| Var | Default | Purpose |
| --- | --- | --- |
| `PORT` | `1234` | websocket port |
| `HOST` | `0.0.0.0` (secure) / forced `127.0.0.1` (dev) | bind address — see below |
| `BOARD_DATA_DIR` | `./.board-data` | local snapshot dir |
| `BOARD_AUTH_SECRET` | — | HMAC token secret. **Set ⇒ SECURE** (token required); unset ⇒ DEV |
| `BOARD_ALLOWED_ORIGINS` | — | Origin allow-list for the WS upgrade (space/comma sep; CSWSH defence). Empty ⇒ not checked |
| `BOARD_MAX_BLOB_BYTES` | `10485760` (10 MiB) | max image dataURL kept in the Y.Doc; larger blobs are pruned |
| `BOARD_MAX_MESSAGE_BYTES` | `16777216` (16 MiB) | max WS frame size (must be ≥ blob cap) |
| `BOARD_MAX_ROOM_CONNS` | `64` | max concurrent connections per room |
| `BOARD_MAX_CONNS` | `1024` | max concurrent connections server-wide |

**Auth (mirrors `server-go/auth.go`).** When `BOARD_AUTH_SECRET` is set the WS
upgrade requires a valid `?token=` whose optional `room` claim matches the joined
room; tokens are the same HMAC shape as the Go server
(`base64url(payload).base64url(HMAC_SHA256(secret, base64url(payload)))`,
`payload = {"exp":…,"room":…}`), compared in constant time. **Tokens travel in
the connection URL** (a y-websocket constraint) so they may land in logs/history
— mint them short-lived, room-scoped and ideally single-use; never pass a
long-lived bearer token (see `WebsocketProviderOptions.token`).

**Dev mode (no secret).** The server refuses to bind anywhere but `127.0.0.1`,
runs without auth, and prints a loud `INSECURE DEV SERVER` warning. This keeps
the current tokenless local Workspace/Talk integrations working without ever
exposing an open, unauthenticated board server on all interfaces.

Demo hosts honour `VITE_BOARD_SERVER` (default `ws://localhost:1234`) and a
`?board=<id>` query param. A **zero-server** variant (`examples/local-doc`,
served at `/local.html`) needs no backend at all — a plain local `Y.Doc` handed
straight to `<BoardApp/>`.

## Development

```bash
npm install
npm run dev         # standalone demo (Vite) on :5174
npm run server      # board sync server on :1234
npm run build       # library bundle -> dist/ (ESM + CJS + .d.ts + board-ui.css)
npm run typecheck   # tsc --noEmit
```

Consume locally from a host repo:

```json
{ "dependencies": { "@vulos/board-ui": "file:../board-ui" } }
```

## Contributing

Issues and PRs welcome at `github.com/vul-os/board-ui`. Keep the public API in
`src/index.ts` stable — host apps integrate against it.

## License

[MIT](./LICENSE) © 2026 Vulos contributors.
