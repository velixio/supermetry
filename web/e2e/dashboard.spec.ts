import { readFileSync } from 'node:fs'

import type { Request } from '@playwright/test'

import { expect, fmtCompact, test } from './fixtures'
import { setTheme } from './theme'

const range = { from: '2026-09-28', to: '2026-09-30' }

test('dashboard numbers equal the API and click-to-filter refetches every card', async ({
  page,
  request,
  owner,
}) => {
  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}?period=custom&from=${range.from}&to=${range.to}`
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

  const response = await request.post('/api/private/query', {
    headers: owner.apiHeaders,
    data: {
      site_id: owner.siteId,
      date_range: range,
      metrics: ['visitors', 'pageviews'],
    },
  })
  const responseBody = await response.text()
  expect(response.ok(), responseBody).toBe(true)
  const api = JSON.parse(responseBody) as {
    totals: { visitors: number; pageviews: number }
  }

  await owner.login(page, dashboard)
  // The plain-language lead above the metrics: a headline, or the few-visitors note on a quiet project.
  await expect(page.getByTestId('dashboard-summary-text')).toContainText(/visited|Not much traffic yet/)
  await expect(page.getByRole('button', { name: /Visitors/ })).toContainText(
    fmtCompact(api.totals.visitors),
  )
  await expect(page.getByRole('button', { name: /Pageviews/ })).toContainText(
    fmtCompact(api.totals.pageviews),
  )

  const indiaFilter = page.getByRole('region', { name: 'Location' }).getByRole('button', { name: /^India/ })
  await expect(indiaFilter).toHaveAttribute('aria-describedby')
  await indiaFilter.click()
  await expect
    .poll(() => new URL(page.url()).searchParams.getAll('f'))
    .toContain('country:is:IN')
  await expect.poll(() => filteredRequests.length).toBeGreaterThanOrEqual(6)
  const filteredResponse = await request.post('/api/private/query', {
    headers: owner.apiHeaders,
    data: {
      site_id: owner.siteId,
      date_range: range,
      metrics: ['visitors', 'pageviews'],
      filters: [{ dimension: 'country', operator: 'is', values: ['IN'] }],
    },
  })
  const filteredBody = await filteredResponse.text()
  expect(filteredResponse.ok(), filteredBody).toBe(true)
  const filtered = JSON.parse(filteredBody) as {
    totals: { visitors: number; pageviews: number }
  }
  await expect(page.getByRole('button', { name: /Visitors/ })).toContainText(
    fmtCompact(filtered.totals.visitors),
  )
  await expect(page.getByRole('button', { name: /Pageviews/ })).toContainText(
    fmtCompact(filtered.totals.pageviews),
  )
  await expect(page.locator('[aria-label^="Loading "]')).toHaveCount(0)

  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.screenshot({ path: 'e2e/screenshots/dashboard-light-1440.png', fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.screenshot({ path: 'e2e/screenshots/dashboard-light-390.png', fullPage: true })
})

test('compare, CSV, theme persistence, and the M shortcut work', async ({ page, owner }) => {
  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}?period=custom&from=2026-09-29&to=2026-09-29`
  await owner.login(page, dashboard)
  await page.goto(dashboard)
  const headerActions = page.getByTestId('filters-trailing')
  await headerActions.getByRole('button', { name: 'View options' }).click()
  await expect(page.getByRole('menuitemcheckbox', { name: 'Include imported data' })).toBeVisible()
  await page.keyboard.press('Escape')
  // Compare lives on the chart it changes and appears when the pointer is over the chart.
  await page.getByTestId('kpi-chart').hover()
  await expect(page.getByTestId('chart-controls').getByRole('combobox', { name: 'Compare' })).toBeVisible()

  await page.getByRole('combobox', { name: 'Compare' }).click()
  await page.getByRole('option', { name: 'Previous period' }).click()
  await expect.poll(() => new URL(page.url()).searchParams.get('compare')).toBe(
    'previous_period',
  )
  await expect(page.locator('[data-trend="good"], [data-trend="bad"]').first()).toBeVisible()

  const countries = page.getByRole('region', { name: 'Location' })
  await countries.getByRole('button', { name: 'View all Location' }).click()
  const downloadPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Download CSV' }).click()
  const download = await downloadPromise
  const path = await download.path()
  expect(path).not.toBeNull()
  const header = readFileSync(path!, 'utf8').split(/\r?\n/, 1)[0]
  expect(header).toMatch(/country/i)

  await page.getByRole('button', { name: 'Close' }).click()
  await setTheme(page, 'dark')
  await expect(page.locator('html')).toHaveClass(/dark/)
  await page.reload()
  await expect(page.locator('html')).toHaveClass(/dark/)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.screenshot({ path: 'e2e/screenshots/dashboard-dark-1440.png', fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.screenshot({ path: 'e2e/screenshots/dashboard-dark-390.png', fullPage: true })

  await page.keyboard.press('m')
  await expect.poll(() => new URL(page.url()).searchParams.get('period')).toBe('30d')
})
