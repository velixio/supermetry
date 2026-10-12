import { existsSync, mkdirSync } from 'node:fs'

import type { Page } from '@playwright/test'

import { removeSavedDashboards } from './dashboards-cleanup'
import { expect, test } from './fixtures'

test.use({ bypassCSP: true })

// axe-core is not a project dependency: point SUPERMETRY_AXE_PATH at axe.min.js to run this audit.
const axePath = process.env.SUPERMETRY_AXE_PATH ?? ''
const shots = 'e2e/screenshots/ui-qa'
const range = 'period=custom&from=2026-09-28&to=2026-09-30'

type Violation = { id: string; impact: string | null; nodes: Array<{ target: string[]; failureSummary?: string }> }

async function audit(page: Page, name: string) {
  await page.addScriptTag({ path: axePath })
  const violations = await page.evaluate(async () => {
    const axe = (window as unknown as { axe: { run(): Promise<{ violations: Violation[] }> } }).axe
    return (await axe.run()).violations
  })
  const bad = violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')
  const detail = bad.map(
    (v) => `${v.id}: ${v.nodes.slice(0, 4).map((n) => `${n.target.join(' ')} ${(n.failureSummary ?? '').split('\n').slice(1, 2).join('')}`).join(' | ')}`,
  )
  expect.soft(detail, `${name} axe serious/critical`).toEqual([])
}

async function noOverflow(page: Page, name: string) {
  const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  const culprits = over > 0
    ? await page.evaluate(() => {
        const width = document.documentElement.clientWidth
        return [...document.body.querySelectorAll('*')]
          .filter((el) => el.getBoundingClientRect().right > width + 1 && !el.closest('[class*="overflow-x-auto"]'))
          .slice(0, 4)
          .map((el) => `${el.tagName.toLowerCase()}.${String(el.getAttribute('class')).slice(0, 60)}`)
      })
    : []
  expect.soft(over, `${name} horizontal overflow ${culprits.join(' | ')}`).toBeLessThanOrEqual(0)
}

async function setTheme(page: Page, theme: 'light' | 'dark') {
  const toDark = page.getByRole('button', { name: 'Use dark theme' })
  const toLight = page.getByRole('button', { name: 'Use light theme' })
  const want = theme === 'dark' ? toDark : toLight
  if (await want.count()) {
    await want.first().click()
  } else if (!(await (theme === 'dark' ? toLight : toDark).count())) {
    throw new Error('no theme toggle found')
  }
  await expect(page.locator('html')).toHaveClass(theme === 'dark' ? /dark/ : /^(?!.*dark)/)
}

type Step = [name: string, url: string, action?: (page: Page) => Promise<void>]

test('new pages pass axe and fit the viewport in light and dark themes', async ({ page, request, owner }) => {
  test.skip(!axePath || !existsSync(axePath), 'set SUPERMETRY_AXE_PATH to an axe-core axe.min.js to run this audit')
  test.setTimeout(900_000)
  mkdirSync(shots, { recursive: true })
  await removeSavedDashboards(request, owner)
  const created = await request.post(`/api/private/sites/${owner.siteId}/dashboards`, {
    headers: owner.apiHeaders,
    data: {
      name: 'QA board',
      layout: {
        schema_version: 1,
        period: '30d',
        widgets: [
          { id: 'kpis', kind: 'kpi_strip', title: 'Headline numbers', x: 0, y: 0, w: 12, h: 2, config: { metrics: ['visitors', 'pageviews'] } },
          { id: 'pages', kind: 'breakdown', title: 'Top pages', x: 0, y: 2, w: 6, h: 5, config: { dimension: 'page', metrics: ['visitors'], limit: 10 } },
          { id: 'trend', kind: 'time_series', title: 'Visitors over time', x: 6, y: 2, w: 6, h: 5, config: { metric: 'visitors' } },
        ],
      },
    },
  })
  expect(created.ok()).toBe(true)
  const dashId = ((await created.json()) as { id: number }).id

  const site = `/app/${owner.orgSlug}/${owner.siteSlug}`
  const steps: Step[] = [
    ['admin-users', '/app/admin/users'],
    ['admin-organizations', '/app/admin/organizations'],
    ['admin-sites', '/app/admin/sites'],
    ['admin-settings', '/app/admin/settings'],
    ['admin-security', '/app/admin/security'],
    ['integrations', `${site}/settings/integrations`],
    ['links', `${site}/links`],
    ['pixel', `${site}/links/pixel`],
    ['cohorts', `${site}/cohorts`],
    ['revenue', `${site}/revenue?${range}`],
    ['dashboard-viewer', `${site}?dash=${dashId}&${range}`],
    ['dashboard-editor', `${site}?dash=${dashId}&${range}`, async (p) => { await p.getByRole('button', { name: 'Edit dashboard' }).click() }],
    [
      'full-report',
      `${site}?${range}`,
      async (p) => {
        await p.getByRole('region', { name: 'Location' }).getByRole('button', { name: 'View full report' }).click()
        await expect(p.getByRole('dialog')).toBeVisible()
      },
    ],
    ['notification-history', `${site}/settings/notification-history`],
    ['notification-templates', `/app/${owner.orgSlug}/settings/notifications`],
    ['sites-overview', '/app'],
    ['sites-list', `/app/${owner.orgSlug}/sites`],
    ['install-recipes', `${site}/settings/installation`],
  ]

  await owner.login(page, '/app')
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: width === 1440 ? 1000 : 900 })
    for (const theme of ['light', 'dark'] as const) {
      for (const [name, url, action] of steps) {
        const label = `${name} ${width} ${theme}`
        await page.goto(url)
        await expect(page.getByRole('heading', { level: 1 }).first()).toBeVisible()
        await setTheme(page, theme)
        if (action) await action(page)
        await page.waitForTimeout(700)
        await page.screenshot({ path: `${shots}/${name}-${width}-${theme}.png` })
        await noOverflow(page, label)
        await audit(page, label)
      }
    }
  }
  await removeSavedDashboards(request, owner)
})
