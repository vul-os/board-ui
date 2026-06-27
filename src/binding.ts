/**
 * src/binding.ts — the Yjs <-> Excalidraw binding.
 *
 * One instance bridges a Y.Doc (the source of truth) and a live Excalidraw
 * editor. It runs in both directions and guards against feedback loops:
 *
 *   local edit  : Excalidraw onChange -> diff vs Y.Map -> write changed/added/
 *                 deleted elements inside a single ydoc.transact(..., origin).
 *   remote edit : ymap.observeDeep -> rebuild the elements array -> updateScene.
 *                 Changes whose transaction origin is *this* binding are
 *                 skipped (we caused them), and updateScene is wrapped so the
 *                 onChange it triggers does not echo back to Yjs.
 *
 * Elements are stored one-per-id in a Y.Map so concurrent edits to different
 * elements merge cleanly. Image blobs (Excalidraw `files`) are mirrored into a
 * second Y.Map keyed by fileId.
 */

import * as Y from 'yjs'
import type { BoardElement, BoardFile } from './types'
import { ELEMENTS_KEY, FILES_KEY } from './doc'

/** The subset of Excalidraw's imperative API the binding needs. */
export interface ExcalidrawSceneAPI {
  updateScene(scene: {
    elements?: readonly BoardElement[]
    collaborators?: Map<string, unknown>
  }): void
  getSceneElementsIncludingDeleted(): readonly BoardElement[]
  addFiles(files: BoardFile[]): void
  getFiles(): Record<string, BoardFile>
}

/**
 * Raster image mime types we accept from remote peers. SVG (image/svg+xml) and
 * markup (text/html, …) are deliberately excluded: they can carry script and
 * would be a stored-XSS vector if a peer-supplied dataURL were ever rendered as
 * markup. Defence-in-depth — a malicious/compromised peer must not be able to
 * push an active-content "image" into our editor via the CRDT.
 */
const ALLOWED_IMAGE_MIME = new Set<string>([
  'image/png',
  'image/jpeg',
  'image/gif',
  'image/webp',
  'image/bmp',
  'image/x-icon',
  'image/avif',
])

/**
 * Accept a remote file only when BOTH its declared `mimeType` and the mime
 * encoded in the dataURL itself are in the raster-image allow-list. Checking the
 * dataURL prefix too stops a `mimeType: image/png` / `dataURL: data:text/html…`
 * mismatch from slipping through.
 */
function isAllowedImage(f: BoardFile): boolean {
  if (!f || !ALLOWED_IMAGE_MIME.has(f.mimeType)) return false
  const m = /^data:([^;,]+)[;,]/.exec(typeof f.dataURL === 'string' ? f.dataURL : '')
  const declared = m?.[1]?.toLowerCase()
  return !!declared && ALLOWED_IMAGE_MIME.has(declared)
}

/** Order elements the way Excalidraw expects (fractional index when present). */
function sortElements(elements: BoardElement[]): BoardElement[] {
  return elements.sort((a, b) => {
    const ai = a.index
    const bi = b.index
    if (ai != null && bi != null) return ai < bi ? -1 : ai > bi ? 1 : 0
    if (ai != null) return -1
    if (bi != null) return 1
    return 0
  })
}

export class ExcalidrawYBinding {
  readonly doc: Y.Doc
  readonly api: ExcalidrawSceneAPI
  readonly yElements: Y.Map<BoardElement>
  readonly yFiles: Y.Map<BoardFile>

  /** Transaction origin tag so we can ignore our own updates on the way back. */
  private readonly origin = Symbol('@vulos/board-ui')
  /** True while we are applying a *remote* change to Excalidraw. */
  private applyingRemote = false
  private disposed = false

  constructor(doc: Y.Doc, api: ExcalidrawSceneAPI) {
    this.doc = doc
    this.api = api
    this.yElements = doc.getMap<BoardElement>(ELEMENTS_KEY)
    this.yFiles = doc.getMap<BoardFile>(FILES_KEY)

    this.yElements.observeDeep(this.onRemoteElements)
    this.yFiles.observe(this.onRemoteFiles)
  }

  /** Push whatever is already in the Y.Doc into a freshly-mounted editor. */
  loadInitial(): void {
    if (this.yElements.size === 0 && this.yFiles.size === 0) return
    this.renderFromDoc()
  }

  /** Excalidraw `onChange` handler — local edits flow into the Y.Doc here. */
  handleChange = (
    elements: readonly BoardElement[],
    _appState: unknown,
    files?: Record<string, BoardFile>,
  ): void => {
    if (this.applyingRemote || this.disposed) return

    this.doc.transact(() => {
      const seen = new Set<string>()
      for (const el of elements) {
        seen.add(el.id)
        const prev = this.yElements.get(el.id)
        // Write only when new or actually changed (Excalidraw bumps `version`).
        if (
          !prev ||
          prev.version !== el.version ||
          prev.versionNonce !== el.versionNonce ||
          prev.isDeleted !== el.isDeleted
        ) {
          this.yElements.set(el.id, el)
        }
      }
      // True removals: an id we track that Excalidraw dropped entirely.
      // (Excalidraw usually keeps deleted elements with isDeleted=true, handled
      // above; this covers the case where they are pruned from the scene.)
      for (const id of [...this.yElements.keys()]) {
        if (!seen.has(id)) this.yElements.delete(id)
      }

      if (files) {
        for (const [id, f] of Object.entries(files)) {
          if (f && !this.yFiles.has(id)) this.yFiles.set(id, f)
        }
      }
    }, this.origin)
  }

  private onRemoteElements = (_events: Y.YEvent<Y.Map<BoardElement>>[], txn: Y.Transaction): void => {
    if (txn.origin === this.origin) return // our own write — already in the editor
    this.renderFromDoc()
  }

  private onRemoteFiles = (_event: Y.YMapEvent<BoardFile>, txn: Y.Transaction): void => {
    if (txn.origin === this.origin) return
    this.pushFiles()
  }

  /** Rebuild the scene from the Y.Doc and apply it without echoing back. */
  private renderFromDoc(): void {
    const elements = sortElements([...this.yElements.values()])
    this.applyingRemote = true
    try {
      this.pushFiles()
      this.api.updateScene({ elements })
    } finally {
      this.applyingRemote = false
    }
  }

  private pushFiles(): void {
    if (this.yFiles.size === 0) return
    const existing = this.api.getFiles()
    const incoming = [...this.yFiles.values()].filter((f) => {
      if (!f || existing[f.id]) return false
      if (!isAllowedImage(f)) {
        // Drop non-image / active-content blobs from peers (e.g. svg+xml,
        // text/html). They never reach Excalidraw's file store.
        console.warn(`[vulos-board] rejected remote file with disallowed mime "${f?.mimeType}"`)
        return false
      }
      return true
    })
    if (incoming.length > 0) this.api.addFiles(incoming)
  }

  destroy(): void {
    this.disposed = true
    this.yElements.unobserveDeep(this.onRemoteElements)
    this.yFiles.unobserve(this.onRemoteFiles)
  }
}
