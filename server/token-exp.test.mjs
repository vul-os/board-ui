/**
 * server/token-exp.test.mjs — SECURE-mode token policy regression test.
 *
 * Verifies that in SECURE mode (BOARD_AUTH_SECRET set) a token's `exp` claim is
 * REQUIRED and short-lived: tokens without `exp`, expired tokens, and tokens
 * whose lifetime exceeds BOARD_MAX_TOKEN_TTL_SECONDS are rejected.
 *
 * Run: node --test server/   (or: npm test)
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import os from 'node:os'
import path from 'node:path'
import crypto from 'node:crypto'

const SECRET = 'test-secret'
const MAX_TTL = 3600

// Configure SECURE mode BEFORE importing the module (it starts on import).
process.env.BOARD_AUTH_SECRET = SECRET
process.env.PORT = '31998'
process.env.HOST = '127.0.0.1'
process.env.BOARD_MAX_TOKEN_TTL_SECONDS = String(MAX_TTL)
process.env.BOARD_DATA_DIR = path.join(os.tmpdir(), `board-token-exp-${process.pid}`)

const { server, verifyToken } = await import('./index.mjs')
server.unref()

function mint(payload) {
  const payloadB64 = Buffer.from(JSON.stringify(payload)).toString('base64url')
  const sig = crypto.createHmac('sha256', SECRET).update(payloadB64).digest('base64url')
  return `${payloadB64}.${sig}`
}

const now = () => Math.floor(Date.now() / 1000)

await test('exp is REQUIRED, must be valid, unexpired, and short-lived', (t) => {
  t.after(() => server.close())

  // Valid short-lived token → accepted.
  assert.equal(verifyToken(mint({ exp: now() + 60 }), 'room1'), true, 'valid short-lived token')

  // Missing exp → rejected (was previously accepted when exp was optional).
  assert.equal(verifyToken(mint({}), 'room1'), false, 'token without exp')
  assert.equal(verifyToken(mint({ room: 'room1' }), 'room1'), false, 'token with room but no exp')

  // Non-numeric / zero exp → rejected.
  assert.equal(verifyToken(mint({ exp: 'soon' }), 'room1'), false, 'non-numeric exp')
  assert.equal(verifyToken(mint({ exp: 0 }), 'room1'), false, 'zero exp')

  // Expired → rejected.
  assert.equal(verifyToken(mint({ exp: now() - 1 }), 'room1'), false, 'expired token')

  // Lifetime longer than the max TTL → rejected (over-long-lived).
  assert.equal(verifyToken(mint({ exp: now() + MAX_TTL + 600 }), 'room1'), false, 'over-long-lived token')

  // Room claim mismatch still rejected.
  assert.equal(verifyToken(mint({ exp: now() + 60, room: 'other' }), 'room1'), false, 'room mismatch')

  // Tampered signature rejected.
  const good = mint({ exp: now() + 60 })
  assert.equal(verifyToken(good.slice(0, -1) + (good.at(-1) === 'A' ? 'B' : 'A'), 'room1'), false, 'bad sig')
})
