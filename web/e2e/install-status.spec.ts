import { expect, test } from './fixtures'

test('fixture site reports Receiving after an accepted event', async ({ page, request, owner }) => {
  const accepted = await request.post('/api/public/v1/events', {
    data: {
      site: owner.siteSlug,
      name: 'pageview',
      url: 'https://site1.test/install-status-check',
    },
  })
  expect(accepted.status()).toBe(202)

  await expect.poll(async () => {
    const response = await request.get(`/api/private/sites/${owner.siteId}`, { headers: owner.apiHeaders })
    expect(response.ok(), await response.text()).toBe(true)
    return (await response.json() as { install_status: string }).install_status
  }).toBe('receiving')

  const consoleErrors: string[] = []
  const serverErrors: string[] = []
  page.on('console', (message) => {
    if (message.type() === 'error' && !message.text().startsWith('Failed to load resource:')) {
      consoleErrors.push(message.text())
    }
  })
  page.on('response', (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`)
  })

  await owner.login(page, `/app/${owner.orgSlug}/${owner.siteSlug}/settings/installation`)
  await expect(page.getByRole('heading', { name: 'Installation' })).toBeVisible()
  await expect(page.getByText('Receiving', { exact: true })).toBeVisible()
  await expect(page.getByText(/^Last event /)).toBeVisible()
  expect(consoleErrors).toEqual([])
  expect(serverErrors).toEqual([])
})
