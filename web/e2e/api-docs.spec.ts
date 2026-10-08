import { expect, test } from './fixtures'

test('API reference renders the embedded contract and runs a private read request', async ({ page, owner }) => {
  const apiReference = `/${owner.orgSlug}/settings/api`
  await owner.login(page, apiReference)

  await expect(page.getByRole('heading', { name: 'API reference', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'GET Get a site', exact: true }).click()
  await expect(page.getByText('/api/public/v1/sites/{site_id}', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Send request' }).click()
  await expect(page.getByLabel('Response')).toContainText('200')

  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.screenshot({ path: 'e2e/screenshots/api-reference.png', fullPage: true })
})
