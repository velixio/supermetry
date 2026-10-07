import { expect, test } from './fixtures'

test('funnels and member settings render for the owner', async ({ page, owner }) => {
  const funnels = `/${owner.orgSlug}/${owner.siteSlug}/funnels`
  await owner.login(page, funnels)

  await expect(page.getByRole('heading', { name: 'Funnels', exact: true })).toBeVisible()
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.screenshot({ path: 'e2e/screenshots/funnels.png', fullPage: true })

  await page.goto(`/${owner.orgSlug}/settings/members`)
  await expect(page.getByRole('heading', { name: 'Members' })).toBeVisible()
  await expect(page.getByRole('paragraph').filter({ hasText: owner.email })).toBeVisible()
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.screenshot({ path: 'e2e/screenshots/settings-members.png', fullPage: true })
})
