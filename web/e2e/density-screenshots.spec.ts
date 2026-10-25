import { mkdirSync } from 'node:fs'

import { expect, test } from './fixtures'

// Before/after captures for layout reviews. Runs only when SUPERMETRY_SCREENSHOT_DIR is set;
// SUPERMETRY_SCREENSHOT_LABEL (for example "before" or "after") prefixes the file names.
const directory = process.env.SUPERMETRY_SCREENSHOT_DIR ?? ''
const label = process.env.SUPERMETRY_SCREENSHOT_LABEL ?? 'current'

test('captures the dashboard, site settings and admin users at desktop and phone widths', async ({ page, owner }) => {
  test.skip(!directory, 'set SUPERMETRY_SCREENSHOT_DIR to capture screenshots')
  mkdirSync(directory, { recursive: true })
  const pages = [
    ['dashboard', `/app/${owner.orgSlug}/${owner.siteSlug}?period=custom&from=2026-09-28&to=2026-09-30`, 'Dashboard'],
    ['site-settings', `/app/${owner.orgSlug}/${owner.siteSlug}/settings/general`, 'Site settings'],
    ['admin-users', '/app/admin/users', 'Users'],
  ] as const
  await owner.login(page, pages[0][1])
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 })
    for (const [name, url, heading] of pages) {
      await page.goto(url)
      await expect(page.getByRole('heading', { level: 1, name: heading })).toBeVisible()
      await page.waitForLoadState('networkidle')
      await page.screenshot({ path: `${directory}/${label}-${name}-${width}.png`, animations: 'disabled' })
    }
  }
})
