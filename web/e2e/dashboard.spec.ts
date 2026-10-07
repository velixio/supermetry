import { readFileSync } from 'node:fs'

import type { Request } from '@playwright/test'

import { expect, fmtCompact, test } from './fixtures'

const range = { from: '2026-09-28', to: '2026-09-30' }

test('dashboard numbers equal the API and click-to-filter refetches every card', async ({
  page,
  request,
  owner,
}) => {
  const filteredRequests: Array<Record<string, unknown>> = []
  page.on('request', async (candidate: Request) => {
    if (!candidate.url().endsWith('/api/private/query') || candidate.method() !== 'POST') {
      return
    }
    const body = candidate.postDataJSON() as Record<string, unknown>
    const filters = body.filters as Array<Record<string, unknown>> | undefined
    if (filters?.some((filter) => filter.dimension === 'country')) {
      filteredRequests.push(body)
    }
  })

  await owner.login(page)
  const response = await request.post('/api/private/query', {
    headers: owner.apiHeaders,
    data: {
      site_id: owner.siteId,
      date_range: range,
      metrics: ['visitors', 'pageviews'],
    },
  })
  expect(response.ok(), await response.text()).toBe(true)
  const api = (await response.json()) as {
    totals: { visitors: number; pageviews: number }
  }

  await page.goto(
    `/${owner.orgSlug}/${owner.siteSlug}?period=custom&from=${range.from}&to=${range.to}`,
  )
  await expect(page.getByRole('button', { name: /Visitors/ })).toContainText(
    fmtCompact(api.totals.visitors),
  )
  await expect(page.getByRole('button', { name: /Pageviews/ })).toContainText(
    fmtCompact(api.totals.pageviews),
  )

  await page.getByRole('button', { name: /filter by country IN/i }).click()
  await expect
    .poll(() => new URL(page.url()).searchParams.getAll('f'))
    .toContain('country:is:IN')
  await expect.poll(() => filteredRequests.length).toBeGreaterThanOrEqual(6)
  await expect(page.getByRole('button', { name: /Visitors/ })).not.toContainText(
    fmtCompact(api.totals.visitors),
  )

  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.screenshot({ path: 'e2e/screenshots/dashboard-light-1440.png', fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.screenshot({ path: 'e2e/screenshots/dashboard-light-390.png', fullPage: true })
})

test('compare, CSV, theme persistence, and the M shortcut work', async ({ page, owner }) => {
  const dashboard = `/${owner.orgSlug}/${owner.siteSlug}?period=custom&from=${range.from}&to=${range.to}`
  await owner.login(page, dashboard)
  await page.goto(dashboard)

  await page.getByLabel('Compare').selectOption('previous_period')
  await expect.poll(() => new URL(page.url()).searchParams.get('compare')).toBe(
    'previous_period',
  )
  await expect(page.locator('[data-trend="good"], [data-trend="bad"]').first()).toBeVisible()

  const countries = page.getByRole('region', { name: 'Countries' })
  await countries.getByRole('button', { name: 'Details' }).click()
  const downloadPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: 'CSV' }).click()
  const download = await downloadPromise
  const path = await download.path()
  expect(path).not.toBeNull()
  const header = readFileSync(path!, 'utf8').split(/\r?\n/, 1)[0]
  expect(header).toMatch(/country/i)

  await page.getByRole('button', { name: 'Close' }).click()
  await page.getByRole('button', { name: 'Use dark theme' }).click()
  await expect(page.locator('html')).toHaveClass(/dark/)
  await page.reload()
  await expect(page.locator('html')).toHaveClass(/dark/)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.screenshot({ path: 'e2e/screenshots/dashboard-dark-1440.png', fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.screenshot({ path: 'e2e/screenshots/dashboard-dark-390.png', fullPage: true })

  await page.keyboard.press('m')
  await expect.poll(() => new URL(page.url()).searchParams.get('period')).toBe('30d')
})
