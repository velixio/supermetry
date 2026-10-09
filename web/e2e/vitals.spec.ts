import { expect, test } from './fixtures'

const response = {
  overall: {
    lcp: { p75: 2800, samples: 20, good: 0.6, needs_improvement: 0.25, poor: 0.15 },
    cls: { p75: 80, samples: 18, good: 0.8, needs_improvement: 0.15, poor: 0.05 },
    inp: { p75: 550, samples: 12, good: 0.25, needs_improvement: 0.5, poor: 0.25 },
  },
  breakdown: [
    {
      value: 'Chrome',
      lcp: { p75: 3100, samples: 10, good: 0.5, needs_improvement: 0.3, poor: 0.2 },
      cls: { p75: 120, samples: 9, good: 0.7, needs_improvement: 0.2, poor: 0.1 },
      inp: { p75: 220, samples: 7, good: 0.4, needs_improvement: 0.4, poor: 0.2 },
    },
  ],
  timeseries: [
    { date: '2026-09-28', lcp: { p50: 2100, p75: 2700, p95: 3900, samples: 6 }, cls: { p50: 50, p75: 80, p95: 130, samples: 5 }, inp: { p50: 170, p75: 220, p95: 480, samples: 4 } },
    { date: '2026-09-29', lcp: { p50: 2200, p75: 2900, p95: 4300, samples: 7 }, cls: { p50: 60, p75: 90, p95: 180, samples: 6 }, inp: { p50: 190, p75: 260, p95: 560, samples: 4 } },
    { date: '2026-09-30', lcp: { p50: 1900, p75: 2400, p95: 3700, samples: 7 }, cls: { p50: 40, p75: 70, p95: 120, samples: 7 }, inp: { p50: 150, p75: 190, p95: 420, samples: 4 } },
  ],
}

test('Web Vitals diagnostic breakdowns work at desktop and mobile widths', async ({ page, owner }) => {
  await page.route('**/api/private/vitals/query', async (route) => {
    const dimension = (route.request().postDataJSON() as { dimension?: string }).dimension
    const value =
      dimension === 'country'
        ? 'US'
        : dimension === 'device'
          ? 'Desktop'
          : dimension === 'page'
            ? '/'
            : 'Chrome'
    await route.fulfill({ json: { ...response, breakdown: [{ ...response.breakdown[0], value }] } })
  })

  const vitals = `/app/${owner.orgSlug}/${owner.siteSlug}/vitals?period=custom&from=2026-09-28&to=2026-09-30`
  await page.setViewportSize({ width: 1440, height: 1000 })
  await owner.login(page, vitals)

  const trend = page.getByRole('region', { name: 'Web Vitals trend' })
  await expect(trend.getByRole('img', { name: 'Largest Contentful Paint daily percentiles' })).toBeVisible()
  await trend.getByRole('tab', { name: 'CLS' }).click()
  await expect(trend.getByRole('img', { name: 'Cumulative Layout Shift daily percentiles' })).toBeVisible()
  await expect(trend.getByText('18 samples shown')).toBeVisible()

  const breakdown = page.getByRole('tablist', { name: 'Vitals breakdown' })
  for (const label of ['Page', 'Device', 'Country', 'Browser']) {
    await expect(breakdown.getByRole('tab', { name: label })).toBeVisible()
  }
  await breakdown.getByRole('tab', { name: 'Country' }).click()
  await expect(page.getByRole('columnheader', { name: 'Country' })).toBeVisible()
  await expect(page.getByRole('cell', { name: 'United States' })).toBeVisible()
  await page.screenshot({ path: 'e2e/screenshots/vitals-country-1440.png', fullPage: true })

  await page.setViewportSize({ width: 390, height: 844 })
  await breakdown.getByRole('tab', { name: 'Browser' }).click()
  await expect(page.getByRole('columnheader', { name: 'Browser' })).toBeVisible()
  await expect(page.getByRole('cell', { name: 'Chrome' })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  await page.screenshot({ path: 'e2e/screenshots/vitals-browser-390.png', fullPage: true })
})
