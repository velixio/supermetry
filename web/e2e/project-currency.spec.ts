import { expect, test } from './fixtures'

test('project settings show configured, unconfigured, and conflict currency states', async ({ page, owner }) => {
  const settings = `/app/${owner.orgSlug}/${owner.siteSlug}/settings/general`
  await owner.login(page, settings)

  await expect(page.getByLabel('Revenue currency', { exact: true })).toHaveValue('USD')
  await expect(page.getByLabel('Revenue currency', { exact: true })).toHaveAttribute('readonly')
  // The explanation lives in the label's InfoTip.
  await page.getByRole('button', { name: 'About Revenue currency' }).click()
  await expect(page.getByText('This currency is permanent for the project.').first()).toBeVisible()

  await page.route(`**/api/private/sites/${owner.siteId}`, async (route) => {
    const response = await route.fetch()
    const site = await response.json()
    await route.fulfill({ response, json: { ...site, revenue_currency: undefined, revenue_currency_state: 'unconfigured' } })
  })
  await page.reload()
  await expect(page.getByLabel('Revenue currency', { exact: true })).toBeEditable()
  await page.getByRole('button', { name: 'About Revenue currency' }).click()
  await expect(page.getByText(/permanent once chosen/i).first()).toBeVisible()

  await page.unroute(`**/api/private/sites/${owner.siteId}`)
  await page.route(`**/api/private/sites/${owner.siteId}`, async (route) => {
    const response = await route.fetch()
    const site = await response.json()
    await route.fulfill({ response, json: { ...site, revenue_currency: undefined, revenue_currency_state: 'conflict' } })
  })
  await page.reload()
  await expect(page.getByRole('alert')).toContainText('Revenue paused')
  await expect(page.getByRole('alert')).toContainText('Other analytics are unaffected')
})
