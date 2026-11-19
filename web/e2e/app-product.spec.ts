import { expect, test } from './fixtures'

const DAY = 24 * 60 * 60 * 1000

test('app project: product screens feed the Users page (DAU 1 today, MAU 4)', async ({ page, owner }) => {
  await owner.login(page, `/app/${owner.orgSlug}/sites`)
  const created = await page.request.post('/api/private/sites', {
    headers: owner.apiHeaders,
    data: { name: 'Desk app', kind: 'app', timezone: 'UTC', retention_days: 365 },
  })
  expect(created.status(), await created.text()).toBe(201)
  const site = await created.json()

  const keyResponse = await page.request.post(`/api/private/sites/${site.id}/ingest-keys`, { headers: owner.apiHeaders })
  expect(keyResponse.status(), await keyResponse.text()).toBe(201)
  const { key } = await keyResponse.json()

  // One install today, three on earlier days within the last week: DAU 1 for today, MAU 4.
  const now = Date.now()
  const events = [0, 1, 2, 3].map((daysAgo, index) => ({
    id: `evt-product-${index}`,
    ts: now - daysAgo * DAY - (daysAgo === 0 ? 1000 : 0),
    kind: 'screen',
    screen: 'Home',
    release: '1.0.0',
    platform: 'ios',
    install_id: `install-product-${index}`,
  }))
  const intake = await page.request.post('/api/public/v1/app-events', {
    headers: { 'X-Supermetry-Key': key },
    data: { site: site.public_id, events },
  })
  expect(intake.status(), await intake.text()).toBe(202)

  await page.goto(`/app/${owner.orgSlug}/${site.slug}/users`)
  await expect(page.getByRole('heading', { name: 'Sessions & users' })).toBeVisible()
  const tiles = page.getByLabel('Active installs', { exact: true })
  await expect(async () => {
    await page.reload()
    await expect(tiles.locator('div', { hasText: 'DAU (latest day)' }).first()).toContainText('1')
    await expect(tiles.locator('div', { hasText: 'MAU' }).first()).toContainText('4')
  }).toPass({ timeout: 30_000 })

  await page.getByRole('tab', { name: 'Platform' }).click()
  await expect(page.getByRole('table', { name: 'Adoption by platform' })).toContainText('ios')
})
