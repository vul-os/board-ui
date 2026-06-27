/**
 * server/conn-cap.test.mjs — regression test for the connection-counter cap.
 *
 * Verifies the per-room connection cap is enforced atomically: even when many
 * upgrades race concurrently, the number of simultaneously OPEN connections to a
 * room never exceeds BOARD_MAX_ROOM_CONNS. This guards against the prior TOCTOU
 * bug where the cap was checked at `upgrade` but incremented only later in the
 * `connection` handler, letting concurrent upgrades all pass the check.
 *
 * Run: node --test server/   (or: npm test)
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import os from 'node:os'
import path from 'node:path'
import { WebSocket } from 'ws'

const PORT = 31999
const ROOM_CAP = 2

// Configure the server BEFORE importing it (the module starts on import).
process.env.PORT = String(PORT)
process.env.HOST = '127.0.0.1'
process.env.BOARD_MAX_ROOM_CONNS = String(ROOM_CAP)
process.env.BOARD_MAX_CONNS = '100'
process.env.BOARD_DATA_DIR = path.join(os.tmpdir(), `board-conn-cap-${process.pid}`)
delete process.env.BOARD_AUTH_SECRET // DEV mode: no token required (127.0.0.1)

const { server } = await import('./index.mjs')
server.unref() // don't let the listener keep the test process alive

await test('per-room connection cap is never exceeded under concurrent upgrades', async (t) => {
  // Wait until the server is actually listening.
  if (!server.listening) await new Promise((r) => server.once('listening', r))

  t.after(() => server.close())

  const N = 12 // far more than the cap, all fired concurrently
  const sockets = []
  const settle = (resolve) => (outcome) => resolve(outcome)

  const results = await Promise.all(
    Array.from({ length: N }, () =>
      new Promise((resolve) => {
        const done = settle(resolve)
        const ws = new WebSocket(`ws://127.0.0.1:${PORT}/cap-room`)
        sockets.push(ws)
        ws.on('open', () => done('open'))
        ws.on('unexpected-response', () => done('rejected'))
        ws.on('error', () => done('rejected'))
      }),
    ),
  )

  const opened = results.filter((r) => r === 'open').length
  const rejected = results.filter((r) => r === 'rejected').length

  for (const ws of sockets) {
    try {
      ws.close()
    } catch {
      /* ignore */
    }
  }

  assert.ok(opened <= ROOM_CAP, `opened=${opened} exceeded room cap ${ROOM_CAP}`)
  assert.equal(opened, ROOM_CAP, `expected exactly ${ROOM_CAP} connections to open, got ${opened}`)
  assert.equal(rejected, N - ROOM_CAP, `expected ${N - ROOM_CAP} rejections, got ${rejected}`)
})
