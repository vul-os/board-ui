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

The bundled `npm run server` persists each room's snapshot. In production it
persists to the Vulos storage bucket under a `board/<room>.bin` key (see the
storage-bucket seam / TODO in `server/index.mjs`).

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

## Configuration

Sync server (`npm run server`) environment:

| Var | Default | Purpose |
| --- | --- | --- |
| `PORT` | `1234` | websocket port |
| `HOST` | `0.0.0.0` | bind address |
| `BOARD_DATA_DIR` | `./.board-data` | local snapshot dir |
| `VULOS_STORAGE_*` | — | storage-bucket seam (see `server/index.mjs` TODO) |

Demo host honours `VITE_BOARD_SERVER` (default `ws://localhost:1234`) and a
`?board=<id>` query param.

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
