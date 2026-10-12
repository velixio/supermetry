import { expect, test } from './fixtures'

for (const width of [1024, 390]) {
  test(`API reference lists every public operation without overflow at ${width}px`, async ({ page, owner }) => {
    const errors: string[] = []
    page.on('pageerror', (error) => errors.push(error.message))
    page.on('console', (message) => {
      if (message.type() === 'error') errors.push(message.text())
    })
    await page.setViewportSize({ width, height: 900 })
    await owner.login(page, `/app/${owner.orgSlug}/settings/api`)
    await expect(page.getByRole('heading', { name: 'API reference', exact: true })).toBeVisible()

    const spec = await (await page.request.get('/api/public/openapi.json')).json()
    const operations = Object.values<Record<string, unknown>>(spec.paths).reduce(
      (total, item) => total + ['get', 'post', 'put', 'patch', 'delete'].filter((method) => method in item).length,
      0,
    )
    const buttons = page.getByRole('navigation', { name: 'API endpoints' }).getByRole('button')
    await expect(buttons).toHaveCount(operations)

    const metrics = await page.evaluate(() => ({
      scrollWidth: document.documentElement.scrollWidth,
      innerWidth: window.innerWidth,
      scrollHeight: document.documentElement.scrollHeight,
    }))
    expect(metrics.scrollWidth).toBeLessThanOrEqual(metrics.innerWidth)
    expect(metrics.scrollHeight).toBeLessThan(6000)

    await page.getByRole('searchbox', { name: 'Search endpoints' }).fill('journey')
    await expect(buttons).toHaveCount(1)
    await buttons.first().click()
    expect(page.url()).toContain('#op-')
    await expect(page.getByText('/journeys', { exact: false }).first()).toBeVisible()
    await page.getByRole('searchbox', { name: 'Search endpoints' }).fill('zzzz-none')
    await expect(page.getByText('No endpoints match your search.')).toBeVisible()
    expect(errors).toEqual([])
  })
}
