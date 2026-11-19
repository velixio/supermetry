import { authenticatedAuditRoutes, axeAvailable, axeMissing, blocking, describeViolations, publicAuditRoutes, runAxe } from './axe'
import { expect, test } from './fixtures'

// Runs axe on every listed route in the default (light) and the dark theme. Serious and critical
// violations fail the test; moderate and minor ones are printed. Run with: bunx playwright test a11y-routes
test.use({ bypassCSP: true })
test.skip(!axeAvailable, axeMissing)

async function settle(page: import('@playwright/test').Page) {
  await page.waitForLoadState('networkidle')
  await expect(page.locator('[aria-busy="true"], [aria-label^="Loading "]')).toHaveCount(0, { timeout: 10_000 })
}

for (const theme of ['light', 'dark'] as const) {
  test.describe(`axe, ${theme} theme`, () => {
    test.beforeEach(async ({ page }) => {
      await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' })
      await page.addInitScript((value) => {
        try { window.localStorage.setItem('theme', value) } catch { /* ignore */ }
      }, theme)
    })

    for (const route of publicAuditRoutes) {
      test(`public ${route}`, async ({ page }) => {
        await page.goto(route)
        await settle(page)
        const violations = await runAxe(page)
        console.log(`AXE ${theme} ${route} ${violations.length} violation(s)`)
        expect(blocking(violations), describeViolations(blocking(violations))).toEqual([])
      })
    }

    for (const template of authenticatedAuditRoutes) {
      test(`app ${template}`, async ({ page, owner }) => {
        const route = template.replace('{org}', owner.orgSlug).replace('{site}', owner.siteSlug)
        await owner.login(page, route)
        await settle(page)
        const violations = await runAxe(page)
        console.log(`AXE ${theme} ${template} ${violations.length} violation(s)`)
        expect(blocking(violations), describeViolations(blocking(violations))).toEqual([])
      })
    }
  })
}
