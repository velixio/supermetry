import { expect, test } from './fixtures'

test('a shared dashboard renders read-only', async ({ page, owner }) => {
  await page.goto(`/share/${owner.shareSlug}`)
  await expect(page.getByRole('heading', { name: 'Shared dashboard' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Analytics summary' })).toBeVisible()
  await expect(page.getByText('Visitors')).toBeVisible()
  await expect(page.getByRole('button')).toHaveCount(0)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.screenshot({ path: 'e2e/screenshots/share.png', fullPage: true })
})
