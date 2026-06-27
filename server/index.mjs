/**
 * server/index.mjs — minimal Vulos board sync server (DEV-ONLY fallback).
 *
 * A standard y-websocket sync server (the same wire protocol
 * createWebsocketProvider speaks) with snapshot persistence, so Vulos
 * Workspace and local dev have a working backend out of the box.
 *
 * ⚠️  This Node server is the **dev / single-node fallback**. The production
 *     board backend is the hardened Go server in `server-go/`. This file is no
 *     longer shipped in the npm package (`files` excludes `server/`) so that
 *     consumers never receive it as a dependency.
 *
 * Each room's state is persisted as a single Y.encodeStateAsUpdate snapshot.
 * Default persistence is local disk: ./.board-data/<room>.bin
 *
 * ── Security model (mirrors server-go/auth.go — seam A) ──────────────────────
 *   • If BOARD_AUTH_SECRET is set the server runs SECURE: every websocket
 *     upgrade must carry a valid `?token=` (HMAC-SHA256, optional exp/room
 *     claims) and the optional `room` claim must match the joined room.
 *   • If BOARD_AUTH_SECRET is unset the server runs in DEV mode: it binds to
 *     127.0.0.1 ONLY (never 0.0.0.0) and prints a loud insecure-server warning.
 *     This keeps the current tokenless Workspace/Talk dev integrations working
 *     locally without ever exposing an open board server on all interfaces.
 *   • BOARD_ALLOWED_ORIGINS (space/comma separated) is an Origin allow-list for
 *     the upgrade (CSWSH defence). Empty ⇒ origins are not checked (dev).
 *   • DoS limits: max WS frame size, per-room + global connection caps, and a
 *     max image/base64 blob size before it is allowed to live in the Y.Doc.
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
 * Run: npm run server   (PORT, HOST, BOARD_DATA_DIR + the BOARD_* security env
 * documented above are honoured)
 */

import http from 'node:http'
import fs from 'node:fs'
import path from 'node:path'
import crypto from 'node:crypto'
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

// ── Configuration ───────────────────────────────────────────────────────────
const AUTH_SECRET = process.env.BOARD_AUTH_SECRET ?? ''
const SECURE = AUTH_SECRET.length > 0

const ALLOWED_ORIGINS = (process.env.BOARD_ALLOWED_ORIGINS ?? '')
  .split(/[\s,]+/)
  .filter(Boolean)

const PORT = Number(process.env.PORT ?? 1234)
// In DEV mode (no secret) we refuse to bind anywhere but loopback so an
// unauthenticated board server is never reachable off-host. Only the SECURE
// mode honours HOST (default 0.0.0.0).
const HOST = SECURE ? (process.env.HOST ?? '0.0.0.0') : '127.0.0.1'

const DATA_DIR = process.env.BOARD_DATA_DIR ?? path.resolve(process.cwd(), '.board-data')
const STORAGE_PREFIX = process.env.VULOS_STORAGE_PREFIX ?? 'board/'

// DoS limits. The WS frame cap must be >= the blob cap (a single image rides in
// one Yjs update / WS message); a frame larger than the cap is dropped by `ws`.
const MAX_BLOB_BYTES = Number(process.env.BOARD_MAX_BLOB_BYTES ?? 10 * 1024 * 1024) // 10 MiB per image dataURL
const MAX_MESSAGE_BYTES = Number(process.env.BOARD_MAX_MESSAGE_BYTES ?? 16 * 1024 * 1024) // 16 MiB WS frame
const MAX_ROOM_CONNS = Number(process.env.BOARD_MAX_ROOM_CONNS ?? 64)
const MAX_TOTAL_CONNS = Number(process.env.BOARD_MAX_CONNS ?? 1024)

// Mirror src/doc.ts FILES_KEY — kept inline so this dev server has no build dep.
const FILES_KEY = 'files'

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

    // DoS / abuse guard: cap image blob size. y-websocket applies peer updates
    // opaquely (we cannot inspect them before they hit the doc), so we prune
    // after-insert — any `files` entry whose dataURL exceeds the cap is deleted
    // and that deletion replicates to every peer. Deleting never re-triggers as
    // oversized (the value is gone), so there is no observer loop.
    const yfiles = ydoc.getMap(FILES_KEY)
    yfiles.observe((event) => {
      const oversized = []
      for (const key of event.keysChanged) {
        const f = yfiles.get(key)
        const dataURL = f && typeof f === 'object' ? f.dataURL : undefined
        if (typeof dataURL === 'string' && dataURL.length > MAX_BLOB_BYTES) {
          oversized.push(key)
        }
      }
      if (oversized.length > 0) {
        ydoc.transact(() => {
          for (const k of oversized) yfiles.delete(k)
        })
        console.warn(
          `[vulos-board] dropped ${oversized.length} oversized blob(s) (> ${MAX_BLOB_BYTES} bytes) in room "${room}"`,
        )
      }
    })

    ydoc.on('update', () => scheduleSave(room, ydoc))
  },
  writeState: async (room, ydoc) => {
    clearTimeout(pending.get(room))
    pending.delete(room)
    saveSnapshot(room, Y.encodeStateAsUpdate(ydoc))
  },
})

// ── Room / auth helpers ─────────────────────────────────────────────────────

/** Derive the room exactly as y-websocket's setupWSConnection does. */
function roomFromReq(req) {
  return (req.url ?? '/').slice(1).split('?')[0]
}

function tokenFromReq(req) {
  try {
    return new URL(req.url ?? '/', 'http://localhost').searchParams.get('token') ?? ''
  } catch {
    return ''
  }
}

/**
 * Verify an HMAC token, byte-compatible with server-go/auth.go:
 *   token       = base64url(payloadJSON) + "." + base64url(HMAC_SHA256(secret, base64url(payloadJSON)))
 *   payloadJSON = {"exp": <unix-seconds, optional>, "room": "<board id, optional>"}
 * `exp` (if present) is enforced; `room` (if non-empty) must equal `room`.
 */
function verifyToken(token, room) {
  if (!token) return false
  const dot = token.indexOf('.')
  if (dot <= 0 || dot === token.length - 1) return false
  const payloadB64 = token.slice(0, dot)
  const sigB64 = token.slice(dot + 1)

  let sig
  try {
    sig = Buffer.from(sigB64, 'base64url')
  } catch {
    return false
  }
  const expected = crypto.createHmac('sha256', AUTH_SECRET).update(payloadB64).digest()
  if (sig.length !== expected.length || !crypto.timingSafeEqual(sig, expected)) return false

  let claims
  try {
    claims = JSON.parse(Buffer.from(payloadB64, 'base64url').toString('utf8'))
  } catch {
    return false
  }
  if (typeof claims !== 'object' || claims === null) return false
  if (claims.exp && Math.floor(Date.now() / 1000) > Number(claims.exp)) return false
  if (claims.room && claims.room !== room) return false
  return true
}

/** CSWSH defence: enforce the Origin allow-list when one is configured. */
function originAllowed(req) {
  if (ALLOWED_ORIGINS.length === 0) return true // not configured (dev)
  const origin = req.headers.origin
  // Browsers always send Origin on a WS handshake; when an allow-list is in
  // force a missing/unlisted Origin is rejected.
  return Boolean(origin) && ALLOWED_ORIGINS.includes(origin)
}

function rejectUpgrade(socket, code, reason) {
  const text =
    { 401: 'Unauthorized', 403: 'Forbidden', 429: 'Too Many Requests', 503: 'Service Unavailable' }[
      code
    ] ?? 'Bad Request'
  socket.write(`HTTP/1.1 ${code} ${text}\r\nConnection: close\r\nContent-Length: 0\r\n\r\n`)
  socket.destroy()
  if (reason) console.warn(`[vulos-board] upgrade rejected (${code}): ${reason}`)
}

// ── HTTP + WebSocket server ─────────────────────────────────────────────────

const server = http.createServer((_req, res) => {
  res.writeHead(200, { 'content-type': 'text/plain' })
  res.end('vulos board sync server — ok\n')
})

// noServer + manual upgrade handling so we can reject (origin / auth / caps)
// BEFORE completing the WebSocket handshake. maxPayload caps WS frame size.
const wss = new WebSocketServer({ noServer: true, maxPayload: MAX_MESSAGE_BYTES })

const roomConns = new Map() // room -> active connection count
let totalConns = 0

server.on('upgrade', (req, socket, head) => {
  if (!originAllowed(req)) {
    rejectUpgrade(socket, 403, `origin "${req.headers.origin ?? ''}" not allowed`)
    return
  }

  const room = roomFromReq(req)

  if (SECURE && !verifyToken(tokenFromReq(req), room)) {
    rejectUpgrade(socket, 401, `invalid/missing token for room "${room}"`)
    return
  }

  if (totalConns >= MAX_TOTAL_CONNS) {
    rejectUpgrade(socket, 503, 'server connection cap reached')
    return
  }
  if ((roomConns.get(room) ?? 0) >= MAX_ROOM_CONNS) {
    rejectUpgrade(socket, 429, `room "${room}" connection cap reached`)
    return
  }

  wss.handleUpgrade(req, socket, head, (conn) => {
    wss.emit('connection', conn, req)
  })
})

wss.on('connection', (conn, req) => {
  const room = roomFromReq(req)
  totalConns += 1
  roomConns.set(room, (roomConns.get(room) ?? 0) + 1)
  conn.on('close', () => {
    totalConns -= 1
    const n = (roomConns.get(room) ?? 1) - 1
    if (n <= 0) roomConns.delete(room)
    else roomConns.set(room, n)
  })
  setupWSConnection(conn, req)
})

server.listen(PORT, HOST, () => {
  console.log(`[vulos-board] sync server on ws://${HOST}:${PORT}  (data: ${DATA_DIR})`)
  if (SECURE) {
    console.log(
      `[vulos-board] SECURE mode: token auth ON` +
        (ALLOWED_ORIGINS.length > 0 ? `, origin allow-list: ${ALLOWED_ORIGINS.join(', ')}` : ', origin check OFF (set BOARD_ALLOWED_ORIGINS)'),
    )
  } else {
    console.warn(
      '\n' +
        '  ************************************************************************\n' +
        '  *  INSECURE DEV SERVER — localhost only, NO AUTHENTICATION.           *\n' +
        '  *  Bound to 127.0.0.1 so it is not reachable off this machine.        *\n' +
        '  *  Set BOARD_AUTH_SECRET (+ BOARD_ALLOWED_ORIGINS) before exposing    *\n' +
        '  *  this server anywhere. For production use the Go server (server-go).*\n' +
        '  ************************************************************************\n',
    )
  }
})
