import { mkdirSync } from 'node:fs'

import { expect, test } from './fixtures'

const report = {
  currency: 'USD', breakdown: 'source', attribution: 'event', lookback_days: 30,
  orders: 12, revenue: 640, average_order_value: 53.33, visitors: 480, revenue_per_visitor: 1.33,
  unique_customers: 9, repeat_customers: 2, repeat_rate: 22.2, truncated: true,
  rows: [
    { key: 'newsletter', orders: 7, revenue: 420, average_order_value: 60, unique_customers: 5, repeat_customers: 2, repeat_rate: 40 },
    { key: 'google', orders: 4, revenue: 160, average_order_value: 40, unique_customers: 3, repeat_customers: 0, repeat_rate: 0 },
    { key: '', orders: 1, revenue: 60, average_order_value: 60, unique_customers: 1, repeat_customers: 0, repeat_rate: 0 },
  ],
}

test('revenue breakdown, attribution and customers', async ({ page, owner }) => {
  const reportUrls: string[] = []
  const accepts: string[] = []
  await page.route('**/api/private/query', (route) => route.fulfill({ json: {
    columns: ['time', 'total_revenue', 'average_revenue'], rows: [{ time: '2026-09-28', total_revenue: 40, average_revenue: 20 }],
    totals: { total_revenue: 640, average_revenue: 53 }, meta: { sources_used: ['closed'], cached: false, imported_included: true },
  } }))
  await page.route('**/revenue/report?*', async (route) => {
    const request = route.request()
    accepts.push(request.headers().accept ?? '')
    if ((request.headers().accept ?? '').includes('text/csv')) {
      await route.fulfill({ status: 200, contentType: 'text/csv', body: 'key,orders,revenue\nnewsletter,7,420\n' })
      return
    }
    reportUrls.push(request.url())
    await route.fulfill({ json: report })
  })
  await page.route('**/revenue/customers?*', (route) => route.fulfill({ json: { data: [
    { profile_id: 1, external_id: 'customer-a', orders: 3, revenue: 210, first_order_at: '2026-09-20T10:00:00Z', last_order_at: '2026-09-29T10:00:00Z' },
  ] } }))

  const url = `/app/${owner.orgSlug}/${owner.siteSlug}/revenue?period=custom&from=2026-09-28&to=2026-09-30&compare=off`
  await page.setViewportSize({ width: 1440, height: 1000 })
  await owner.login(page, url)

  await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1)
  await expect(page.getByRole('heading', { name: 'Revenue by breakdown' })).toBeVisible()
  await expect(page.getByRole('rowheader', { name: /Direct or unknown/ })).toBeVisible()
  await expect(page.getByText(/capped at 20,000 orders/i)).toBeVisible()
  await expect(page.getByText('Revenue per visitor')).toBeVisible()
  await expect(page.getByRole('rowheader', { name: 'customer-a' })).toBeVisible()

  await page.getByLabel('Attribution').selectOption('last_click')
  await page.getByLabel('Break down by').selectOption('campaign')
  await expect.poll(() => reportUrls.some((u) => u.includes('breakdown=campaign') && u.includes('attribution=last_click') && u.includes('lookback_days=30'))).toBe(true)

  const download = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export CSV' }).click()
  expect((await download).suggestedFilename()).toMatch(/^revenue-campaign-2026-09-28-2026-09-30\.csv$/)
  expect(accepts.some((a) => a.includes('text/csv'))).toBe(true)

  // Keyboard reachability and target size.
  await page.getByLabel('Break down by').focus()
  await page.keyboard.press('Tab')
  await expect(page.getByLabel('Attribution')).toBeFocused()
  for (const name of ['Break down by', 'Attribution', 'Lookback days']) {
    const box = await page.getByLabel(name).boundingBox()
    expect(box?.height ?? 0).toBeGreaterThanOrEqual(44)
  }
  const exportBox = await page.getByRole('button', { name: 'Export CSV' }).boundingBox()
  expect(exportBox?.height ?? 0).toBeGreaterThanOrEqual(44)

  const dir = 'e2e/screenshots/revenue-report'
  mkdirSync(dir, { recursive: true })
  await page.screenshot({ path: `${dir}/1440-light.png`, fullPage: true })
  await page.emulateMedia({ colorScheme: 'dark' })
  await page.evaluate(() => { document.documentElement.classList.add('dark') })
  await page.screenshot({ path: `${dir}/1440-dark.png`, fullPage: true })
  await page.evaluate(() => { document.documentElement.classList.remove('dark') })
  await page.emulateMedia({ colorScheme: 'light' })
  for (const width of [1024, 390]) {
    await page.setViewportSize({ width, height: 900 })
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
    await page.screenshot({ path: `${dir}/${width}.png`, fullPage: true })
  }
})

test('revenue breakdown handles forbidden customers and currency conflict', async ({ page, owner }) => {
  await page.route('**/api/private/query', (route) => route.fulfill({ json: {
    columns: ['time', 'total_revenue', 'average_revenue'], rows: [], totals: { total_revenue: 0, average_revenue: 0 },
    meta: { sources_used: ['closed'], cached: false, imported_included: true },
  } }))
  await page.route('**/revenue/report?*', (route) => route.fulfill({ json: { ...report, truncated: false } }))
  await page.route('**/revenue/customers?*', (route) => route.fulfill({ status: 403, contentType: 'application/problem+json', body: JSON.stringify({ detail: 'forbidden' }) }))
  await owner.login(page, `/app/${owner.orgSlug}/${owner.siteSlug}/revenue?period=custom&from=2026-09-28&to=2026-09-30&compare=off`)
  await expect(page.getByText(/needs the profiles permission/i)).toBeVisible()
  await expect(page.getByLabel('Search customers')).toHaveCount(0)
})
