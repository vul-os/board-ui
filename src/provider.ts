/**
 * src/provider.ts — convenience websocket transport for standalone hosts.
 *
 * Only standalone hosts (e.g. Vulos Workspace pointing at the bundled board
 * sync server) use this. Hosts that own their own transport — Meet (LiveKit
 * data channel) and Talk (Vulos Relay) — do NOT use this; they implement their
 * own Y.Doc provider that pumps Y.encodeStateAsUpdate / Y.applyUpdate diffs
 * over their channel. See the README "Embedding" section.
 */

import { WebsocketProvider } from 'y-websocket'
import type { Doc } from 'yjs'

export interface WebsocketProviderOptions {
  /** ws(s):// URL of the board sync server. */
  url: string
  /** Room name — typically the boardId. */
  room: string
  /** The board Y.Doc to sync. */
  doc: Doc
  /**
   * Optional auth token, forwarded as a `?token=` query param.
   *
   * SECURITY: this is inherent to the y-websocket wire protocol — the token can
   * only travel in the connection URL, where it may be captured in proxy/server
   * access logs, browser history and `Referer`. Therefore the token MUST be
   * short-lived (minutes) and ideally single-use / room-scoped: mint it
   * per-board, just-in-time, with a near-term `exp` and a `room` claim (see the
   * HMAC token shape in server-go/auth.go and the Node dev server). Never pass a
   * long-lived session/bearer token here.
   */
  token?: string
}

export function createWebsocketProvider(opts: WebsocketProviderOptions): WebsocketProvider {
  const { url, room, doc, token } = opts
  return new WebsocketProvider(url, room, doc, {
    connect: true,
    params: token ? { token } : {},
  })
}
