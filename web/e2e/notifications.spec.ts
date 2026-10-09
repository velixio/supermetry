import { expect, test } from './fixtures'

const fakeToken = '123456:fake-token-value-for-tests'

test('creates and safely edits a disabled Telegram notification channel', async ({ page, owner }) => {
  await owner.login(page, `/app/${owner.orgSlug}/settings/notifications`)
  await expect(page.getByRole('heading', { name: 'Notifications' })).toBeVisible()

  await page.getByRole('button', { name: 'Add channel' }).click()
  await page.getByLabel('Channel type').selectOption('telegram')
  await page.getByLabel('Name', { exact: true }).fill('Telegram incidents')
  await page.getByLabel('Bot token').fill(fakeToken)
  await page.getByLabel('Chat ID or channel username').fill('-1001234567890')
  await page.getByLabel('Enabled').uncheck()
  const createResponse = page.waitForResponse((response) =>
    response.url().endsWith(`/api/private/orgs/${owner.orgId}/channels`) &&
    response.request().method() === 'POST',
  )
  await page.getByRole('button', { name: 'Create channel' }).click()
  expect((await createResponse).ok()).toBe(true)

  const channel = page.locator('section').filter({ hasText: 'Telegram incidents' })
  await expect(channel).toContainText('Disabled')
  await channel.getByRole('button', { name: 'Edit' }).click()
  await expect(page.getByLabel('Bot token')).toHaveValue('')
  await page.getByLabel('Chat ID or channel username').fill('@supermetry_alerts')
  await page.getByLabel('Enabled').check()
  const updateResponse = page.waitForResponse((response) =>
    /\/api\/private\/orgs\/\d+\/channels\/\d+$/.test(new URL(response.url()).pathname) &&
    response.request().method() === 'PATCH',
  )
  await page.getByRole('button', { name: 'Save changes' }).click()
  expect((await updateResponse).ok()).toBe(true)
  await expect(channel).toContainText('@supermetry_alerts')
  await expect(channel).toContainText('Enabled')

  const listResponse = await page.request.get(`/api/private/orgs/${owner.orgId}/channels`, {
    headers: owner.apiHeaders,
  })
  const responseText = await listResponse.text()
  expect(listResponse.ok(), responseText).toBe(true)
  expect(responseText).not.toContain(fakeToken)
  expect(responseText).not.toContain('bot_token')
})
