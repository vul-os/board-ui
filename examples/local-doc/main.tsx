/**
 * examples/local-doc/main.tsx — zero-server, single-client board.
 *
 * The smallest possible host: NO provider, NO websocket, NO awareness — just a
 * local Y.Doc handed straight to <BoardApp/>. This proves the component is
 * usable fully standalone (the binding keeps Excalidraw <-> Y.Doc in sync for
 * snapshot/persistence) without any backend at all.
 *
 *   npm run dev   ->  open http://localhost:5174/local.html
 *
 * To persist across reloads a host would `encodeSnapshot(ydoc)` to its own
 * storage and `applySnapshot(ydoc, bytes)` on load; this demo keeps it in
 * memory so each reload starts blank.
 */

import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import { BoardApp, createBoardDoc, type BoardUser } from '../../src/index'

const user: BoardUser = { id: crypto.randomUUID(), name: 'Local', color: '#2563eb' }

// A plain local document — no transport is wired to it.
const ydoc = createBoardDoc()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    {/* No `awareness` prop ⇒ solo mode (no remote cursors), no server needed. */}
    <BoardApp ydoc={ydoc} user={user} boardId="local-doc" />
  </StrictMode>,
)
