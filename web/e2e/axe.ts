import { existsSync } from 'node:fs'
import { createRequire } from 'node:module'

import type { Page } from '@playwright/test'

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

// ---- Shared audit helpers (used by a11y-routes.spec.ts; the integrator runs them) ----

export type AxeImpact = 'minor' | 'moderate' | 'serious' | 'critical'
export type AxeNode = { target: string[]; html: string; failureSummary?: string }
export type AxeViolation = { id: string; impact: AxeImpact | null; help: string; nodes: AxeNode[] }

/** Impacts that fail the build. Moderate and minor are reported but do not fail. */
export const blockingImpacts: readonly AxeImpact[] = ['serious', 'critical']

// WCAG 2.0/2.1/2.2 A and AA rules, plus axe best practices.
export const axeTags = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa', 'best-practice']

/** Runs axe on the current page (inject once per navigation) and returns all violations. */
export async function runAxe(page: Page, options: { exclude?: string[] } = {}): Promise<AxeViolation[]> {
  if (!axeAvailable) throw new Error(axeMissing)
  await page.addScriptTag({ path: axePath })
  return page.evaluate(
    async ({ tags, exclude }) => {
      const axe = (window as unknown as {
        axe: { run(ctx: unknown, opts: unknown): Promise<{ violations: AxeViolation[] }> }
      }).axe
      const context = exclude.length ? { exclude: exclude.map((s) => [s]) } : document
      return (await axe.run(context, { runOnly: { type: 'tag', values: tags } })).violations
    },
    { tags: axeTags, exclude: options.exclude ?? [] },
  )
}

export function blocking(violations: AxeViolation[]): AxeViolation[] {
  return violations.filter((v) => v.impact !== null && blockingImpacts.includes(v.impact))
}

/** One line per blocking violation, for the assertion message. */
export function describeViolations(violations: AxeViolation[]): string {
  return violations
    .map((v) => `${v.impact} ${v.id}: ${v.help} (${v.nodes.length} node(s), e.g. ${v.nodes[0]?.target.join(' ')})`)
    .join('\n')
}

/**
 * Route templates for the all-routes audit. `{org}` and `{site}` are filled from the e2e owner.
 * Add a path here when a route is added; routes that need extra fixture ids are left out on purpose.
 */
export const authenticatedAuditRoutes: string[] = [
  '/app/{org}/{site}',
  '/app/{org}/{site}/realtime',
  '/app/{org}/{site}/sessions',
  '/app/{org}/{site}/users',
  '/app/{org}/{site}/events',
  '/app/{org}/{site}/goals',
  '/app/{org}/{site}/funnels',
  '/app/{org}/{site}/journeys',
  '/app/{org}/{site}/retention',
  '/app/{org}/{site}/cohorts',
  '/app/{org}/{site}/revenue',
  '/app/{org}/{site}/vitals',
  '/app/{org}/{site}/releases',
  '/app/{org}/{site}/settings/general',
  '/app/{org}/{site}/settings/installation',
  '/app/{org}/{site}/settings/integrations',
  '/app/{org}/{site}/settings/imports',
  '/app/{org}/settings/members',
  '/app/{org}/settings/invites',
  '/app/{org}/settings/api-keys',
  '/app/{org}/settings/audit',
  '/app/{org}/settings/notifications',
  '/app/{org}/settings/replay-storage',
  '/app/account/profile',
  '/app/account/security',
  '/app/account/sessions',
  '/app/account/notifications',
]

export const publicAuditRoutes: string[] = ['/login', '/signup', '/forgot']
