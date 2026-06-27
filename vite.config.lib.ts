/**
 * vite.config.lib.ts — library build for @vulos/board-ui
 *
 * Produces dist/ with ESM + CJS bundles, generated .d.ts types, and a single
 * bundled stylesheet (dist/board-ui.css). Externalizes react/react-dom and the
 * Excalidraw editor + Yjs so consumers dedupe a single instance.
 *
 * Usage: vite build --config vite.config.lib.ts
 */

import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import dts from 'vite-plugin-dts'
import { resolve } from 'path'

const dir = import.meta.dirname

export default defineConfig({
  plugins: [react(), dts({ include: ['src'], exclude: ['src/vite-env.d.ts'] })],
  build: {
    lib: {
      entry: resolve(dir, 'src/index.ts'),
      formats: ['es', 'cjs'],
      cssFileName: 'board-ui',
      fileName: (format) => (format === 'es' ? 'index.js' : 'index.cjs'),
    },
    outDir: 'dist',
    emptyOutDir: true,
    cssCodeSplit: false,
    sourcemap: true,
    rollupOptions: {
      // Host provides these — keep one copy of React, Excalidraw and Yjs.
      external: [
        'react',
        'react-dom',
        'react/jsx-runtime',
        'react-dom/client',
        '@excalidraw/excalidraw',
        'yjs',
        'y-websocket',
        'y-protocols',
        'y-protocols/awareness',
      ],
      output: {
        exports: 'named',
        globals: {
          react: 'React',
          'react-dom': 'ReactDOM',
          'react/jsx-runtime': 'ReactJSXRuntime',
        },
      },
    },
  },
})
