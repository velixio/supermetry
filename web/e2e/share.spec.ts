import { expect, test } from './fixtures'

test('a shared dashboard renders read-only', async ({ page, owner }) => {
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.emulateMedia({ colorScheme: 'light' })
  await page.goto(`/share/${owner.shareSlug}`)
  await expect(page.getByRole('heading', { name: 'Shared dashboard' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Analytics summary' })).toBeVisible()
  await expect(page.getByText('Visitors')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Pages' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Sources' })).toBeVisible()
  await expect(page.locator('section[aria-busy="false"]')).toHaveCount(9)
  await expect(page.getByRole('button')).toHaveCount(0)
  await expect(page.locator('body')).not.toHaveCSS('overflow-x', 'scroll')
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.screenshot({ path: 'e2e/screenshots/shares/desktop-light-1440.png', fullPage: true })

  await page.emulateMedia({ colorScheme: 'dark' })
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Shared dashboard' })).toBeVisible()
  await expect(page.locator('section[aria-busy="false"]')).toHaveCount(9)
  await page.screenshot({ path: 'e2e/screenshots/shares/desktop-dark-1440.png', fullPage: true })

  for (const [name, width, height] of [['tablet', 1024, 900], ['mobile', 390, 844]] as const) {
    await page.setViewportSize({ width, height })
    await page.reload()
    await expect(page.getByRole('heading', { name: 'Shared dashboard' })).toBeVisible()
    await expect(page.locator('section[aria-busy="false"]')).toHaveCount(9)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)
    expect(overflow).toBe(false)
    await page.screenshot({ path: `e2e/screenshots/shares/${name}-dark-${width}.png`, fullPage: true })
  }
})
