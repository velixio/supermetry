import type { APIRequestContext } from '@playwright/test'

import { removeSavedDashboards } from './dashboards-cleanup'
import { expect, test, type Owner } from './fixtures'

const range = { from: '2026-09-28', to: '2026-09-30' }
const shots = 'e2e/screenshots/dash-view'

type Created = { id: number }

async function createFunnel(request: APIRequestContext, owner: Owner): Promise<number> {
  const response = await request.post(`/api/private/sites/${owner.siteId}/funnels`, {
    headers: owner.apiHeaders,
    data: {
      name: 'Home to pricing',
      steps: [
        { type: 'page', match: 'exact', value: '/' },
        { type: 'page', match: 'prefix', value: '/pricing' },
      ],
    },
  })
  expect(response.ok(), await response.text()).toBe(true)
  return ((await response.json()) as Created).id
}

async function createDashboard(request: APIRequestContext, owner: Owner, name: string, layout: unknown): Promise<number> {
  const response = await request.post(`/api/private/sites/${owner.siteId}/dashboards`, {
    headers: owner.apiHeaders,
    data: { name, layout },
  })
  expect(response.ok(), await response.text()).toBe(true)
  return ((await response.json()) as Created).id
}

function fullLayout(funnelId: number) {
  return {
    schema_version: 1,
    period: '30d',
    widgets: [
      { id: 'kpis', kind: 'kpi_strip', title: 'Headline numbers', x: 0, y: 0, w: 12, h: 2, config: { metrics: ['visitors', 'pageviews', 'bounce_rate'] } },
      { id: 'trend', kind: 'time_series', title: 'Visitors over time', x: 0, y: 2, w: 8, h: 5, config: { metric: 'visitors' } },
      { id: 'pages', kind: 'breakdown', title: 'Top pages', x: 8, y: 2, w: 4, h: 5, period: '7d', config: { dimension: 'page', metrics: ['visitors'], limit: 10 } },
      { id: 'where', kind: 'map', x: 0, y: 7, w: 7, h: 5, config: { metric: 'visitors' } },
      { id: 'rhythm', kind: 'weekly_rhythm', x: 7, y: 7, w: 5, h: 5, config: { metric: 'visitors' } },
      { id: 'goals', kind: 'goals', x: 0, y: 12, w: 6, h: 4, config: { limit: 5 } },
      { id: 'funnel', kind: 'funnel', title: 'Signup funnel', x: 6, y: 12, w: 6, h: 4, config: { funnel_id: funnelId } },
      { id: 'retention', kind: 'retention', x: 0, y: 16, w: 6, h: 4 },
      { id: 'vitals', kind: 'web_vitals', x: 6, y: 16, w: 6, h: 4 },
    ],
  }
}

test.beforeEach(({ request, owner }) => removeSavedDashboards(request, owner))
test.afterEach(({ request, owner }) => removeSavedDashboards(request, owner))

test('a saved dashboard renders every widget kind with overrides, switcher and responsive grid', async ({ page, request, owner }) => {
  const funnelId = await createFunnel(request, owner)
  const id = await createDashboard(request, owner, 'Marketing', fullLayout(funnelId))
  const base = `/app/${owner.orgSlug}/${owner.siteSlug}`
  const queryBodies: Array<{ dimensions?: string[]; date_range: { from: string; to: string } }> = []
  page.on('request', (candidate) => {
    if (candidate.url().endsWith('/api/private/query') && candidate.method() === 'POST') {
      queryBodies.push(candidate.postDataJSON() as (typeof queryBodies)[number])
    }
  })

  await page.setViewportSize({ width: 1440, height: 1000 })
  await owner.login(page, `${base}?dash=${id}&period=custom&from=${range.from}&to=${range.to}`)

  await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1)
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Dashboard')
  const grid = page.getByRole('group', { name: 'Dashboard widgets' })
  await expect(grid).toBeVisible()
  await expect(grid.locator('[data-widget-id]')).toHaveCount(9)
  await expect(grid.getByRole('region', { name: 'Top pages' })).toBeVisible()
  await expect(grid.getByRole('region', { name: 'Signup funnel' })).toBeVisible()
  await expect(grid.getByRole('region', { name: 'Goals' })).toBeVisible()
  await expect(grid.getByRole('region', { name: 'Retention' })).toBeVisible()
  await expect(grid.getByRole('region', { name: 'Web Vitals' })).toBeVisible()
  await expect(grid.getByRole('img', { name: /visitors/i }).first()).toBeVisible()
  await expect(grid.getByText('Headline numbers', { exact: true })).toBeAttached()

  // The widget with its own period asks for a seven day window, the others use the URL range.
  await expect.poll(() => queryBodies.some((body) => body.dimensions?.includes('page') && body.date_range.to !== range.to)).toBe(true)
  const pages = queryBodies.find((body) => body.dimensions?.includes('page') && body.date_range.to !== range.to)
  const days = (Date.parse(`${pages?.date_range.to}T00:00:00Z`) - Date.parse(`${pages?.date_range.from}T00:00:00Z`)) / 86_400_000 + 1
  expect(days).toBe(7)
  expect(queryBodies.some((body) => body.date_range.from === range.from && body.date_range.to === range.to)).toBe(true)

  // Keyboard: open the switcher with the keyboard and choose the standard dashboard.
  const switcher = page.getByRole('combobox', { name: 'Dashboard' })
  await expect(switcher).toBeVisible()
  expect((await switcher.boundingBox())?.height ?? 0).toBeGreaterThanOrEqual(36)
  await switcher.focus()
  await page.keyboard.press('Enter')
  await page.getByRole('option', { name: 'Standard dashboard' }).click()
  await expect(page).toHaveURL(/dash=builtin/)
  await expect(page.getByRole('group', { name: 'Dashboard widgets' })).toHaveCount(0)
  await expect(page.getByRole('region', { name: 'Pages' })).toBeVisible()
  await switcher.focus()
  await page.keyboard.press('Enter')
  await page.getByRole('option', { name: 'Marketing' }).click()
  await expect(page).toHaveURL(new RegExp(`dash=${id}`))
  await expect(page.getByRole('group', { name: 'Dashboard widgets' }).locator('[data-widget-id]')).toHaveCount(9)
  // The dashboard's own default period was adopted into the URL.
  await expect(page).toHaveURL(/period=30d/)

  // Screenshots and responsive checks.
  const url = `${base}?dash=${id}&period=custom&from=${range.from}&to=${range.to}`
  for (const [name, width, height, scheme] of [
    ['1440-light', 1440, 1000, 'light'],
    ['1440-dark', 1440, 1000, 'dark'],
    ['1024', 1024, 900, 'light'],
    ['390', 390, 900, 'light'],
  ] as const) {
    await page.emulateMedia({ colorScheme: scheme })
    await page.setViewportSize({ width, height })
    await page.goto(url)
    await expect(page.getByRole('group', { name: 'Dashboard widgets' })).toBeVisible()
    await expect(page.getByRole('region', { name: 'Web Vitals' })).toBeVisible()
    await page.waitForTimeout(1200)
    if (width === 390) {
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      expect(overflow).toBeLessThanOrEqual(0)
      const columns = await page.evaluate(() => {
        const items = [...document.querySelectorAll('[data-widget-id]')] as HTMLElement[]
        return new Set(items.map((item) => Math.round(item.getBoundingClientRect().left))).size
      })
      expect(columns).toBe(1)
    }
    if (width === 1440) {
      const lefts = await page.evaluate(() => {
        const items = [...document.querySelectorAll('[data-widget-id]')] as HTMLElement[]
        return new Set(items.map((item) => Math.round(item.getBoundingClientRect().left))).size
      })
      expect(lefts).toBeGreaterThan(1)
    }
    await page.screenshot({ path: `${shots}/${name}.png`, fullPage: true })
  }
})

test('the site default dashboard opens on its own and failures stay inside their widget', async ({ page, request, owner }) => {
  const base = `/app/${owner.orgSlug}/${owner.siteSlug}`
  const id = await createDashboard(request, owner, 'Ops', {
    schema_version: 1,
    period: '7d',
    compare: 'previous_period',
    widgets: [
      { id: 'pages', kind: 'breakdown', title: 'Top pages', x: 0, y: 0, w: 6, h: 4, config: { dimension: 'page', metrics: ['visitors'] } },
      { id: 'kpis', kind: 'kpi_strip', title: 'Headline numbers', x: 6, y: 0, w: 6, h: 2, config: { metrics: ['visitors'] } },
    ],
  })
  const flagged = await request.post(`/api/private/sites/${owner.siteId}/dashboards/${id}/default`, { headers: owner.apiHeaders })
  expect(flagged.ok(), await flagged.text()).toBe(true)

  await page.route('**/api/private/query', async (route) => {
    const body = route.request().postDataJSON() as { dimensions?: string[] }
    if (body.dimensions?.includes('page')) {
      await route.fulfill({ status: 500, contentType: 'application/problem+json', body: JSON.stringify({ title: 'Boom', status: 500, detail: 'The pages query failed.' }) })
      return
    }
    await route.continue()
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  await owner.login(page, base)

  await expect(page).toHaveURL(new RegExp(`dash=${id}`))
  await expect(page).toHaveURL(/period=7d/)
  await expect(page).toHaveURL(/compare=previous_period/)
  const grid = page.getByRole('group', { name: 'Dashboard widgets' })
  await expect(grid.getByRole('alert')).toContainText('The pages query failed.')
  await expect(grid.getByText('Headline numbers', { exact: true })).toBeAttached()
  await expect(grid.getByRole('button', { name: 'Retry' })).toBeVisible()

  // A deleted dashboard id shows a clear message and a way back.
  await page.goto(`${base}?dash=999999`)
  await expect(page.getByRole('heading', { name: 'This dashboard no longer exists' })).toBeVisible()
  await page.getByRole('button', { name: 'Open the standard dashboard' }).click()
  await expect(page).toHaveURL(/dash=builtin/)
})
