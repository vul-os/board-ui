/**
 * examples/standalone/main.tsx — tiny standalone host.
 *
 * Proves <BoardApp/> runs end-to-end: it creates a board Y.Doc, connects it to
 * the bundled dev sync server via createWebsocketProvider, wires an Awareness
 * for live cursors, and mounts the board. This is exactly the "Workspace" host
 * integration mode in miniature.
 *
 *   Terminal 1:  npm run server
 *   Terminal 2:  npm run dev   ->  open http://localhost:5174
 *
 * Open it in two tabs to watch elements + cursors sync.
 */

import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import { BoardApp, createBoardDoc, createWebsocketProvider, type BoardUser } from '../../src/index'

const SERVER = import.meta.env.VITE_BOARD_SERVER ?? 'ws://localhost:1234'
const boardId = new URLSearchParams(location.search).get('board') ?? 'demo-board'

const COLORS = ['#e11d48', '#2563eb', '#16a34a', '#d97706', '#7c3aed']
const user: BoardUser = {
  id: crypto.randomUUID(),
  name: 'Guest ' + Math.floor(Math.random() * 1000),
  color: COLORS[Math.floor(Math.random() * COLORS.length)],
}

const ydoc = createBoardDoc()
// The websocket provider owns an Awareness that it syncs over the socket;
// hand that one to BoardApp so cursors propagate between tabs/clients.
const provider = createWebsocketProvider({ url: SERVER, room: boardId, doc: ydoc })
const awareness = provider.awareness

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BoardApp ydoc={ydoc} awareness={awareness} user={user} boardId={boardId} />
  </StrictMode>,
)
