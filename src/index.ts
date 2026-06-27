/**
 * src/index.ts — @vulos/board-ui public barrel.
 *
 * The board document is a Y.Doc. This library owns the Excalidraw editor, the
 * Yjs<->Excalidraw binding, cursor/presence rendering and snapshot
 * encode/decode. The HOST owns the transport (creates the Y.Doc, connects a
 * provider) and persistence. See README "Embedding" for the three host modes.
 *
 * Import the stylesheet once in your host app:
 *   import '@vulos/board-ui/style.css'
 */

export type { BoardUser } from './types'

// The main component.
export { BoardApp, default } from './BoardApp'
export type { BoardAppProps } from './BoardApp'

// Yjs doc helpers (hosts/servers create + snapshot docs uniformly).
export {
  createBoardDoc,
  encodeSnapshot,
  applySnapshot,
  ELEMENTS_KEY,
  FILES_KEY,
} from './doc'

// Convenience transport for standalone hosts (Workspace).
export { createWebsocketProvider } from './provider'
export type { WebsocketProviderOptions } from './provider'

// Advanced: the binding + presence primitives, for hosts building custom shells.
export { ExcalidrawYBinding } from './binding'
export type { ExcalidrawSceneAPI } from './binding'
export { bindAwareness } from './presence'
export type { PresenceBinding, PointerUpdate } from './presence'
