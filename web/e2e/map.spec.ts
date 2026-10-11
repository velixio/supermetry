import { expect, test } from './fixtures'

const screenshotDir = process.env.SUPERMETRY_MAP_SCREENSHOT_DIR ?? 'e2e/screenshots/map-ui'

test('map, country details, and traffic tooltips work at desktop and mobile widths', async ({ page, owner }) => {
  const url = `/app/${owner.orgSlug}/${owner.siteSlug}/map?period=custom&from=2026-09-28&to=2026-09-30&metric=visitors`
  await page.setViewportSize({ width: 1440, height: 900 })
  await owner.login(page, url)

  const country = page.locator('path[role="button"][data-country]').first()
  await expect(country).toBeVisible()
  await country.hover()
  await expect(page.getByRole('tooltip')).toBeVisible()
  await country.click()
  await expect(page.getByRole('button', { name: 'Filter the dashboard to this country' })).toBeVisible()

  const traffic = page.locator('[role="gridcell"][aria-label]:not([aria-label*=" 0 visitors"])').first()
  await expect(traffic).toBeVisible()
  await traffic.hover()
  await expect(page.getByRole('tooltip')).toBeVisible()

  await page.screenshot({
    path: `${screenshotDir}/map-1440-light.png`,
  })

  await page.getByRole('button', { name: 'Use dark theme' }).click()
  await expect(page.locator('html')).toHaveClass(/dark/)
  await traffic.hover()
  await page.screenshot({ path: `${screenshotDir}/map-1440-dark.png` })

  await page.getByRole('button', { name: 'Use light theme' }).click()
  await page.setViewportSize({ width: 1024, height: 900 })
  await traffic.hover()
  await expect(page.getByRole('tooltip')).toBeVisible()
  await page.screenshot({ path: `${screenshotDir}/map-1024-light.png` })

  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.getByRole('dialog')).toBeVisible()
  await expect(traffic).toHaveAttribute('aria-label', /[1-9][0-9,.]* visitors/)
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  await page.screenshot({
    path: `${screenshotDir}/map-390-light.png`,
  })
})
