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

  const vitals = `/${owner.orgSlug}/${owner.siteSlug}/vitals?period=custom&from=2026-09-28&to=2026-09-30`
  await page.setViewportSize({ width: 1440, height: 1000 })
  await owner.login(page, vitals)

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
