/**
 * src/types.ts — shared public types for @vulos/board-ui.
 */

/** A collaborator on a board. `color` drives the cursor/selection tint. */
export interface BoardUser {
  id: string
  name: string
  color?: string
}

/**
 * Minimal structural view of an Excalidraw element. We deliberately avoid
 * importing Excalidraw's deep element type here so the binding stays robust
 * across Excalidraw releases — only `id`, `version` and `isDeleted` are load
 * bearing for the CRDT diff. Everything else rides along untouched.
 */
export interface BoardElement {
  id: string
  version: number
  versionNonce?: number
  isDeleted?: boolean
  /** Fractional index used by Excalidraw to order elements (newer versions). */
  index?: string
  [key: string]: unknown
}

/** Excalidraw binary file (image blob) payload, keyed by fileId. */
export interface BoardFile {
  id: string
  mimeType: string
  dataURL: string
  created: number
  [key: string]: unknown
}
