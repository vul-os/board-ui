/**
 * vite.config.ts — dev/demo build for @vulos/board-ui
 *
 * Serves the standalone example (examples/standalone) so you can prove the
 * <BoardApp/> component runs against the local dev sync server. For the
 * publishable library bundle use vite.config.lib.ts (`npm run build`).
 */

import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: { port: 5174 },
})
