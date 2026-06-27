/**
 * server/index.mjs — minimal Vulos board sync server.
 *
 * A standard y-websocket sync server (the same wire protocol
 * createWebsocketProvider speaks) with snapshot persistence, so Vulos
 * Workspace and local dev have a working backend out of the box.
 *
 * Each room's state is persisted as a single Y.encodeStateAsUpdate snapshot.
 * Default persistence is local disk: ./.board-data/<room>.bin
 *
 * ── Vulos storage-bucket seam ───────────────────────────────────────────────
 * In production this should persist to the Vulos storage bucket instead of
 * disk, under a per-room key with a `board/` prefix:  board/<room>.bin
 * The bucket is reached via the standard `X-Vulos-Storage-*` request headers /
 * env (see the Vulos storage bucket seam). Swap `loadSnapshot`/`saveSnapshot`
 * for bucket GET/PUT against:
 *
 *   VULOS_STORAGE_ENDPOINT   e.g. https://storage.<instance>/board
 *   VULOS_STORAGE_BUCKET     the bucket name
 *   VULOS_STORAGE_TOKEN      bearer/credential for X-Vulos-Storage-Authorization
 *   VULOS_STORAGE_PREFIX     defaults to "board/"
 *
 * TODO(vulos-storage): implement the bucket-backed persistence adapter; the
 * disk adapter below is the local-dev / single-node fallback.
 * ────────────────────────────────────────────────────────────────────────────
 *
 * Run: npm run server   (PORT, HOST, BOARD_DATA_DIR env honoured)
 */

import http from 'node:http'
import fs from 'node:fs'
import path from 'node:path'
import { createRequire } from 'node:module'
import { WebSocketServer } from 'ws'

// y-websocket ships its server helpers as a CommonJS side module that does
// `require('yjs')`. Load yjs through the SAME (CJS) instance so the docs it
// hands us pass Yjs constructor checks (avoids the dual-package hazard /
// "Yjs was already imported" breakage).
const require = createRequire(import.meta.url)
const Y = require('yjs')
let setupWSConnection, setPersistence
try {
  ;({ setupWSConnection, setPersistence } = require('y-websocket/bin/utils'))
} catch {
  ;({ setupWSConnection, setPersistence } = require('@y/websocket-server/utils'))
}

const PORT = Number(process.env.PORT ?? 1234)
const HOST = process.env.HOST ?? '0.0.0.0'
const DATA_DIR = process.env.BOARD_DATA_DIR ?? path.resolve(process.cwd(), '.board-data')
const STORAGE_PREFIX = process.env.VULOS_STORAGE_PREFIX ?? 'board/'

fs.mkdirSync(DATA_DIR, { recursive: true })

/** Per-room key — mirrors the bucket layout (board/<room>.bin). */
function roomFile(room) {
  const safe = String(room).replace(/[^a-zA-Z0-9._-]/g, '_')
  return path.join(DATA_DIR, `${safe}.bin`)
}

function loadSnapshot(room) {
  // TODO(vulos-storage): bucket GET `${STORAGE_PREFIX}${room}.bin`
  void STORAGE_PREFIX
  const file = roomFile(room)
  return fs.existsSync(file) ? fs.readFileSync(file) : null
}

function saveSnapshot(room, bytes) {
  // TODO(vulos-storage): bucket PUT `${STORAGE_PREFIX}${room}.bin`
  fs.writeFileSync(roomFile(room), bytes)
}

// Debounced writer so we are not hammering storage on every keystroke.
const pending = new Map()
function scheduleSave(room, ydoc) {
  clearTimeout(pending.get(room))
  pending.set(
    room,
    setTimeout(() => {
      pending.delete(room)
      saveSnapshot(room, Y.encodeStateAsUpdate(ydoc))
    }, 800),
  )
}

setPersistence({
  provider: 'vulos-board-disk',
  bindState: async (room, ydoc) => {
    const snapshot = loadSnapshot(room)
    if (snapshot) Y.applyUpdate(ydoc, snapshot)
    ydoc.on('update', () => scheduleSave(room, ydoc))
  },
  writeState: async (room, ydoc) => {
    clearTimeout(pending.get(room))
    pending.delete(room)
    saveSnapshot(room, Y.encodeStateAsUpdate(ydoc))
  },
})

const server = http.createServer((_req, res) => {
  res.writeHead(200, { 'content-type': 'text/plain' })
  res.end('vulos board sync server — ok\n')
})

const wss = new WebSocketServer({ server })
wss.on('connection', (conn, req) => {
  // TODO(auth): validate the `?token=` query param against the host before
  // joining the room. board-ui forwards it via createWebsocketProvider.
  setupWSConnection(conn, req)
})

server.listen(PORT, HOST, () => {
  console.log(`[vulos-board] sync server on ws://${HOST}:${PORT}  (data: ${DATA_DIR})`)
})
