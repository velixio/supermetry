import { expect, test } from './fixtures'

// Contract check against the real server: the dashboard client and the revenue
// endpoints must agree on parameter names and response shape.
test('revenue report endpoints answer the dashboard client against the real API', async ({ page, owner }) => {
  const base = `/api/private/sites/${owner.siteId}/revenue`
  const range = 'from=2026-09-28&to=2026-09-30'
  for (const breakdown of ['country', 'region', 'source', 'campaign', 'page', 'product']) {
    const response = await page.request.get(`${base}/report?${range}&breakdown=${breakdown}&attribution=last_click&lookback_days=30&limit=10`, { headers: owner.apiHeaders })
    expect(response.status(), `${breakdown}: ${await response.text()}`).toBe(200)
    const body = await response.json()
    expect(body).toMatchObject({ breakdown, attribution: 'last_click', lookback_days: 30, truncated: false })
    expect(Array.isArray(body.rows)).toBe(true)
    expect(typeof body.orders).toBe('number')
  }
  const csv = await page.request.get(`${base}/report?${range}&breakdown=country`, { headers: { ...owner.apiHeaders, Accept: 'text/csv' } })
  expect(csv.status()).toBe(200)
  expect(csv.headers()['content-type']).toContain('text/csv')
  expect(await csv.text()).toMatch(/^country,orders,revenue,average_order_value/)

  const bad = await page.request.get(`${base}/report?${range}&breakdown=browser`, { headers: owner.apiHeaders })
  expect(bad.status()).toBe(400)
  const customers = await page.request.get(`${base}/customers?${range}`, { headers: owner.apiHeaders })
  // The e2e server runs identified mode with profiles disabled, so customers are refused, not an error.
  expect([200, 403]).toContain(customers.status())
})
