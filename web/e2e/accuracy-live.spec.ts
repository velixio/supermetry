import { mkdirSync } from 'node:fs'

import { expect, test } from './fixtures'

test('explains the live definition at every required viewport', async ({ page, owner }) => {
  const output = 'e2e/screenshots/accuracy-live'
  mkdirSync(output, { recursive: true })
  const realtime = `/app/${owner.orgSlug}/${owner.siteSlug}/realtime`

  await page.addInitScript(() => { if (!localStorage.getItem('theme')) localStorage.setItem('theme', 'light') })
  await page.setViewportSize({ width: 1440, height: 900 })
  await owner.login(page, realtime)
  await expect(page.getByRole('heading', { name: 'Realtime' })).toBeVisible()
  await expect(page.getByText('Distinct visitors with an accepted pageview or custom event in the last 5 minutes.', { exact: true })).toBeVisible()
  await expect(page.getByText('Privacy signals, bots, and site exclusions are not counted.', { exact: true })).toBeVisible()
  await page.screenshot({ path: `${output}/1440-light.png`, fullPage: true })

  await page.evaluate(() => localStorage.setItem('theme', 'dark'))
  await page.reload()
  await expect(page.locator('html')).toHaveClass(/dark/)
  await page.screenshot({ path: `${output}/1440-dark.png`, fullPage: true })

  for (const [name, width] of [['1024', 1024], ['390', 390]] as const) {
    await page.setViewportSize({ width, height: 900 })
    await expect(page.getByRole('heading', { name: 'Realtime' })).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
    await page.screenshot({ path: `${output}/${name}.png`, fullPage: true })
  }
})
