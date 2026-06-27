/**
 * src/presence.ts — render remote cursors/selection from a y-protocols
 * Awareness instance into Excalidraw's collaborator layer.
 *
 * Each client publishes two awareness fields:
 *   user     : { id, name, color }            (set once, stable)
 *   presence : { pointer, button, selectedElementIds }  (updated on move)
 *
 * Remote states are projected into Excalidraw's `collaborators` map via
 * updateScene, which draws their cursors, names and selections natively.
 * Awareness is OPTIONAL — when absent the board still works solo (the editor
 * is bound to the Y.Doc for persistence regardless).
 */

import type { Awareness } from 'y-protocols/awareness'
import type { BoardUser } from './types'

/** Pointer payload Excalidraw hands us from onPointerUpdate. */
export interface PointerUpdate {
  pointer: { x: number; y: number }
  button: 'down' | 'up'
}

interface PresenceState {
  pointer?: { x: number; y: number }
  button?: 'down' | 'up'
  selectedElementIds?: Record<string, boolean>
}

interface AwarenessState {
  user?: BoardUser
  presence?: PresenceState
}

/** The slice of Excalidraw's API presence needs. */
export interface PresenceSceneAPI {
  updateScene(scene: { collaborators?: Map<string, unknown> }): void
}

export interface PresenceBinding {
  /** Wire to Excalidraw's `onPointerUpdate`. */
  onPointerUpdate(payload: PointerUpdate): void
  /** Wire to Excalidraw's `onChange` to publish the local selection. */
  setSelected(selectedElementIds: Record<string, boolean>): void
  destroy(): void
}

export function bindAwareness(
  awareness: Awareness,
  api: PresenceSceneAPI,
  user: BoardUser,
): PresenceBinding {
  awareness.setLocalStateField('user', user)

  let selected: Record<string, boolean> = {}

  const render = (): void => {
    const collaborators = new Map<string, unknown>()
    awareness.getStates().forEach((raw, clientId) => {
      if (clientId === awareness.clientID) return
      const state = raw as AwarenessState
      const u = state.user
      const p = state.presence
      collaborators.set(String(clientId), {
        id: u?.id,
        username: u?.name,
        color: u?.color ? { background: u.color, stroke: u.color } : undefined,
        pointer: p?.pointer,
        button: p?.button ?? 'up',
        selectedElementIds: p?.selectedElementIds,
      })
    })
    api.updateScene({ collaborators })
  }

  awareness.on('change', render)
  render()

  return {
    onPointerUpdate({ pointer, button }: PointerUpdate) {
      const presence: PresenceState = { pointer, button, selectedElementIds: selected }
      awareness.setLocalStateField('presence', presence)
    },
    setSelected(selectedElementIds: Record<string, boolean>) {
      selected = selectedElementIds
      const prev = (awareness.getLocalState() as AwarenessState | null)?.presence ?? {}
      awareness.setLocalStateField('presence', { ...prev, selectedElementIds })
    },
    destroy() {
      awareness.off('change', render)
      awareness.setLocalState(null)
    },
  }
}
