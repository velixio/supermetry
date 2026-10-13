import path from 'node:path'
import { fileURLToPath } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

const rootDir = path.dirname(fileURLToPath(import.meta.url))

// `make dev` serves the UI from Vite and forwards everything the Go server owns.
// The tracker and event paths are configurable, so they follow the server's env.
const devApi = process.env.SUPERMETRY_DEV_API || 'http://127.0.0.1:8787'
const trackerPath = process.env.SUPERMETRY_TRACKER_PATH || '/js/script.js'
const eventPath = process.env.SUPERMETRY_EVENT_PATH || '/api/public/v1/events'
const trackerDir = trackerPath.slice(0, trackerPath.lastIndexOf('/') + 1) || '/'
const fullTrackerPath = trackerPath.replace(/\.js$/, '') + '.full.js'
const exact = (p: string) => `^${p.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`
// Keys starting with ^ are regular expressions; the rest match by prefix.
const goPaths = [
  '^/api(/|$)', // private and public APIs, replay intake, openapi.json
  exact('/healthz'),
  exact('/readyz'),
  exact('/metrics'),
  exact('/replay.js'),
  '/l/', // short links
  '/px/', // tracking pixel
  exact(trackerPath),
  exact(fullTrackerPath),
  // Per-site scripts live next to the tracker, unless it sits at the root.
  ...(trackerDir === '/' ? [] : [trackerDir]),
  exact(eventPath),
]
// Origin and Cookie pass through untouched, so the server's Origin check sees the
// browser's origin (SUPERMETRY_BASE_URL) and cookies stay first-party on the Vite host.
const proxy = Object.fromEntries(
  [...new Set(goPaths)].map((p) => [p, { target: devApi, changeOrigin: true }]),
)

export default defineConfig({
  plugins: [
    tanstackRouter({
      target: 'react',
      autoCodeSplitting: true,
    }),
    react(),
    tailwindcss(),
  ],
  resolve: {
    alias: {
      '@': path.resolve(rootDir, 'src'),
    },
  },
  // strictPort: if :5173 is taken, fail instead of moving to another port, which would
  // break the Origin check (SUPERMETRY_BASE_URL is http://localhost:5173 in make dev).
  server: { host: 'localhost', port: 5173, strictPort: true, proxy },
  build: {
    manifest: true,
    rollupOptions: {
      output: {
        // Keep the universally-needed runtime in two stable requests while
        // leaving charts and route-only libraries behind dynamic boundaries.
        manualChunks(id) {
          if (/[\\/]node_modules[\\/](react|react-dom|scheduler)[\\/]/.test(id)) return 'vendor-react'
          if (/[\\/]node_modules[\\/]@tanstack[\\/](react-query|query-core|react-router|router-core|history|react-store|store)[\\/]/.test(id)) return 'vendor-tanstack'
          if (/[\\/]node_modules[\\/](@radix-ui|radix-ui)[\\/]/.test(id)) return 'vendor-ui'
          if (/[\\/]node_modules[\\/]lucide-react[\\/]/.test(id)) return 'vendor-icons'
          if (/[\\/]src[\\/]components[\\/]ui[\\/](button|dropdown-menu|input|separator|skeleton|tooltip)\.tsx$/.test(id)) return 'ui-shell'
        },
      },
    },
  },
})
