/**
 * src/doc.ts — Yjs document helpers for a Vulos board.
 *
 * The board document is a Y.Doc. Scene elements live in a Y.Map under
 * ELEMENTS_KEY (one entry per element id) so each element merges
 * independently — that is the whole point versus Excalidraw's last-write-wins
 * element array. Image blobs live in a second Y.Map under FILES_KEY.
 *
 * Hosts and the sync server create/snapshot docs through these helpers so the
 * wire format is identical everywhere (Workspace websocket, Meet LiveKit data
 * channel, Talk relay).
 */

import * as Y from 'yjs'

/** Y.Map<string, BoardElement> key holding the scene. */
export const ELEMENTS_KEY = 'elements'

/** Y.Map<string, BoardFile> key holding image blobs (Excalidraw `files`). */
export const FILES_KEY = 'files'

/** Create an empty board document. */
export function createBoardDoc(): Y.Doc {
  const doc = new Y.Doc()
  // Touch the shared types so they exist (and replicate) from the start.
  doc.getMap(ELEMENTS_KEY)
  doc.getMap(FILES_KEY)
  return doc
}

/** Encode the full document state as a Yjs update (a transportable snapshot). */
export function encodeSnapshot(doc: Y.Doc): Uint8Array {
  return Y.encodeStateAsUpdate(doc)
}

/** Apply a snapshot (or any Yjs update) into a document. */
export function applySnapshot(doc: Y.Doc, bytes: Uint8Array): void {
  Y.applyUpdate(doc, bytes)
}
