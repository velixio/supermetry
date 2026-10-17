import { existsSync } from 'node:fs'
import { createRequire } from 'node:module'

// axe-core is a dev dependency (MPL-2.0, test-only, never shipped), so the audits run in the
// normal e2e path. SUPERMETRY_AXE_PATH still points them at another axe.min.js build.
function resolveAxe(): string {
  const override = process.env.SUPERMETRY_AXE_PATH
  if (override) return override
  try {
    return createRequire(import.meta.url).resolve('axe-core/axe.min.js')
  } catch {
    return ''
  }
}

export const axePath = resolveAxe()
export const axeAvailable = axePath !== '' && existsSync(axePath)
export const axeMissing = 'axe-core is not installed: run bun install, or set SUPERMETRY_AXE_PATH to an axe.min.js'
