/**
 * src/BoardApp.tsx — the main collaborative-whiteboard component.
 *
 * The HOST owns the transport: it creates the Y.Doc, connects a provider
 * (websocket / LiveKit data channel / relay) and handles persistence.
 * <BoardApp/> owns the editor: it mounts Excalidraw, binds it to the Y.Doc
 * (per-element CRDT merge) and renders remote cursors from the optional
 * Awareness. This separation is what lets the same component embed into
 * Workspace, Meet and Talk without forking.
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import { Excalidraw } from '@excalidraw/excalidraw'
import '@excalidraw/excalidraw/index.css'

import type { Doc } from 'yjs'
import type { Awareness } from 'y-protocols/awareness'

import type { BoardUser } from './types'
import { ExcalidrawYBinding, type ExcalidrawSceneAPI } from './binding'
import { bindAwareness, type PresenceBinding, type PointerUpdate } from './presence'

export interface BoardAppProps {
  /** The board document. The host creates this and connects a provider. */
  ydoc: Doc
  /** Optional presence channel for cursors/selection. Board works without it. */
  awareness?: Awareness
  /** The local collaborator. */
  user: BoardUser
  /** Stable id for this board (room/channel). Used for presence + diagnostics. */
  boardId: string
  /** Render read-only (Excalidraw viewModeEnabled). */
  readOnly?: boolean
}

export function BoardApp(props: BoardAppProps): JSX.Element {
  const { ydoc, awareness, user, boardId, readOnly } = props

  // Excalidraw hands us its imperative API once mounted.
  const [api, setApi] = useState<ExcalidrawSceneAPI | null>(null)
  const bindingRef = useRef<ExcalidrawYBinding | null>(null)
  const presenceRef = useRef<PresenceBinding | null>(null)

  // Bind Excalidraw <-> Y.Doc once the editor API is available.
  useEffect(() => {
    if (!api) return
    const binding = new ExcalidrawYBinding(ydoc, api)
    bindingRef.current = binding
    binding.loadInitial()
    return () => {
      binding.destroy()
      bindingRef.current = null
    }
  }, [api, ydoc])

  // Wire awareness/presence when provided.
  useEffect(() => {
    if (!api || !awareness) return
    const presence = bindAwareness(awareness, api, user)
    presenceRef.current = presence
    return () => {
      presence.destroy()
      presenceRef.current = null
    }
    // boardId is intentionally in deps so a board switch re-binds presence.
  }, [api, awareness, user, boardId])

  const onChange = useCallback(
    (elements: readonly unknown[], appState: unknown, files: unknown) => {
      bindingRef.current?.handleChange(
        elements as never,
        appState,
        files as never,
      )
      const selected = (appState as { selectedElementIds?: Record<string, boolean> } | undefined)
        ?.selectedElementIds
      if (selected) presenceRef.current?.setSelected(selected)
    },
    [],
  )

  const onPointerUpdate = useCallback((payload: PointerUpdate) => {
    presenceRef.current?.onPointerUpdate(payload)
  }, [])

  return (
    <div className="vulos-board" style={{ width: '100%', height: '100%' }} data-board-id={boardId}>
      <Excalidraw
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        excalidrawAPI={(a: any) => setApi(a as ExcalidrawSceneAPI)}
        onChange={onChange as never}
        onPointerUpdate={onPointerUpdate as never}
        viewModeEnabled={readOnly}
        isCollaborating={Boolean(awareness)}
      />
    </div>
  )
}

export default BoardApp
