import { execFileSync } from 'node:child_process'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

import { devAllowedHosts } from './dev-hosts'

const rootDir = path.dirname(fileURLToPath(import.meta.url))

// The latest release tag, shown next to "dev" on development builds (see src/lib/version.ts).
function releaseTag(): string {
  try {
    return execFileSync('git', ['describe', '--tags', '--abbrev=0', '--match', 'v[0-9]*'], { cwd: rootDir, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim()
  } catch {
    return ''
  }
}

export default defineConfig({
  plugins: [
    tanstackRouter({
      target: 'react',
      autoCodeSplitting: true,
    }),
    react(),
    tailwindcss(),
  ],
  define: {
    __SUPERMETRY_RELEASE_TAG__: JSON.stringify(releaseTag()),
  },
  resolve: {
    alias: {
      '@': path.resolve(rootDir, 'src'),
    },
  },
  // make dev: the Go server (:8787) reverse-proxies the UI to this server, so the
  // browser only uses :8787. strictPort: fail loudly if :5173 is taken, because the
  // Go proxy expects the UI there. allowedHosts extends Vite's host check (never
  // disables it) with the SUPERMETRY_BASE_URL hostname, see dev-hosts.ts.
  server: { host: 'localhost', port: 5173, strictPort: true, allowedHosts: devAllowedHosts(process.env) },
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
