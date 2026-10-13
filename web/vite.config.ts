import path from 'node:path'
import { fileURLToPath } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

const rootDir = path.dirname(fileURLToPath(import.meta.url))

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
  // make dev: the Go server (:8787) reverse-proxies the UI to this server, so the
  // browser only uses :8787. strictPort: fail loudly if :5173 is taken, because the
  // Go proxy expects the UI there.
  server: { host: 'localhost', port: 5173, strictPort: true },
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
