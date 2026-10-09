import { expect, test } from './fixtures'

test('project settings show configured, unconfigured, and conflict currency states', async ({ page, owner }) => {
  const settings = `/${owner.orgSlug}/${owner.siteSlug}/settings/general`
  await owner.login(page, settings)

  await expect(page.getByLabel('Revenue currency')).toHaveValue('USD')
  await expect(page.getByLabel('Revenue currency')).toHaveAttribute('readonly')
  await expect(page.getByText('This currency is permanent for the project.')).toBeVisible()

  await page.route(`**/api/private/sites/${owner.siteId}`, async (route) => {
    const response = await route.fetch()
    const site = await response.json()
    await route.fulfill({ response, json: { ...site, revenue_currency: undefined, revenue_currency_state: 'unconfigured' } })
  })
  await page.reload()
  await expect(page.getByLabel('Revenue currency')).toBeEditable()
  await expect(page.getByText(/choice is permanent/i)).toBeVisible()

  await page.unroute(`**/api/private/sites/${owner.siteId}`)
  await page.route(`**/api/private/sites/${owner.siteId}`, async (route) => {
    const response = await route.fetch()
    const site = await response.json()
    await route.fulfill({ response, json: { ...site, revenue_currency: undefined, revenue_currency_state: 'conflict' } })
  })
  await page.reload()
  await expect(page.getByRole('alert')).toContainText('Revenue reporting paused')
  await expect(page.getByRole('alert')).toContainText('Ordinary analytics remain available')
})
