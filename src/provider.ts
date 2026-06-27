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
  /** Optional auth token, forwarded as a `?token=` query param. */
  token?: string
}

export function createWebsocketProvider(opts: WebsocketProviderOptions): WebsocketProvider {
  const { url, room, doc, token } = opts
  return new WebsocketProvider(url, room, doc, {
    connect: true,
    params: token ? { token } : {},
  })
}
