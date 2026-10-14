import { expect, test } from './fixtures'

const camoUA = 'github-camo (876de43e)'

test('pixel project: create, add a pixel, count a Camo view, see it on the dashboard', async ({ page, owner }) => {
  await owner.login(page, `/app/${owner.orgSlug}/sites`)
  const created = await page.request.post('/api/private/sites', {
    headers: owner.apiHeaders,
    data: { name: 'README views', kind: 'pixel', timezone: 'UTC', retention_days: 365 },
  })
  expect(created.status(), await created.text()).toBe(201)
  const site = await created.json()
  expect(site.kind).toBe('pixel')

  const pixel = await page.request.post(`/api/private/sites/${site.id}/pixels`, { headers: owner.apiHeaders, data: { name: 'GitHub profile' } })
  expect(pixel.status(), await pixel.text()).toBe(201)
  const { public_id: publicId } = await pixel.json()

  const svg = await page.request.get(`/px/${publicId}.svg`, { headers: { 'User-Agent': camoUA } })
  expect(svg.status()).toBe(200)
  expect(svg.headers()['content-type']).toBe('image/svg+xml')
  expect(await svg.text()).toContain('<svg')

  await expect.poll(async () => {
    const list = await (await page.request.get(`/api/private/sites/${site.id}/pixels`, { headers: owner.apiHeaders })).json()
    return list[0]?.total as number
  }, { timeout: 20_000 }).toBe(1)

  await page.goto(`/app/${owner.orgSlug}/${site.slug}`)
  await expect(page.getByText('GitHub profile')).toBeVisible()
  await expect(page.getByText(/views, not unique visitors/)).toBeVisible()
  await expect(page.getByRole('link', { name: /Sessions/ })).toHaveCount(0)

  const unknown = await page.request.get('/px/pZZZZZZZZZZZ.gif', { headers: { 'User-Agent': camoUA } })
  expect(unknown.status()).toBe(200)
  expect(unknown.headers()['content-type']).toBe('image/gif')
})
