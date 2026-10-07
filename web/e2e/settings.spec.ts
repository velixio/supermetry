import { expect, test } from './fixtures'

test('funnels and member settings render for the owner', async ({ page, owner }) => {
  await owner.login(page)

  await page.goto(`/${owner.orgSlug}/${owner.siteSlug}/funnels`)
  await expect(page.getByRole('heading', { name: 'Funnels' })).toBeVisible()
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.screenshot({ path: 'e2e/screenshots/funnels.png', fullPage: true })

  await page.goto(`/${owner.orgSlug}/settings/members`)
  await expect(page.getByRole('heading', { name: 'Members' })).toBeVisible()
  await expect(page.getByText(owner.email)).toBeVisible()
  await page.screenshot({ path: 'e2e/screenshots/settings-members.png', fullPage: true })
})
