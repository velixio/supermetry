import { expect, test } from './fixtures'

test('revenue report is currency-safe and responsive', async ({ page, owner }) => {
  const requests: string[] = []
  await page.route('**/api/private/query', async (route) => {
    const body = route.request().postDataJSON() as { dimensions?: string[] }
    const dimension = body.dimensions?.[0] ?? ''
    requests.push(dimension)
    const attributed = dimension !== 'time'
    const label = dimension === 'source' ? 'Newsletter' : dimension === 'channel' ? 'Email' : 'launch'
    await route.fulfill({ json: {
      columns: [dimension, 'total_revenue', 'average_revenue'],
      rows: dimension === 'time'
        ? [{ time: '2026-09-28', total_revenue: 40, average_revenue: 20 }, { time: '2026-09-29', total_revenue: 60, average_revenue: 30 }]
        : [{ [dimension]: label, total_revenue: 70, average_revenue: 23.33 }],
      totals: { total_revenue: attributed ? 70 : 100, average_revenue: 25 },
      meta: { sources_used: ['closed'], cached: false, imported_included: true, ...(attributed ? { event_attribution: { basis: 'event', coverage: 'available_only', legacy_excluded: true } } : {}) },
      comparison: { date_range: { from: '2026-09-25', to: '2026-09-27' }, rows: [], totals: { total_revenue: 80, average_revenue: 20 }, ...(attributed ? { event_attribution: { basis: 'event', coverage: 'exact', legacy_excluded: false } } : {}) },
    } })
  })

  const revenue = `/app/${owner.orgSlug}/${owner.siteSlug}/revenue?period=custom&from=2026-09-28&to=2026-09-30&compare=previous_period`
  await page.setViewportSize({ width: 1440, height: 1000 })
  await owner.login(page, revenue)

  await expect(page.getByRole('heading', { name: 'Revenue', exact: true })).toBeVisible()
  await expect(page.getByText('$100.00', { exact: true })).toBeVisible()
  await expect(page.getByText('+25% vs previous period').first()).toBeVisible()
  await expect(page.getByText(/available attributed revenue only/i).first()).toBeVisible()
  await expect(page.getByRole('rowheader', { name: 'Newsletter' })).toBeVisible()
  expect(requests.sort()).toEqual(['channel', 'source', 'time', 'utm_campaign'])

  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.getByRole('heading', { name: 'Campaign' })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  await page.screenshot({ path: 'e2e/screenshots/revenue-390.png', fullPage: true })
})
