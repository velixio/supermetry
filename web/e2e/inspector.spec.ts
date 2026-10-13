// Edited but NOT run: the Playwright webServer cannot log in to the database in the authoring environment.
import { expect, test } from './fixtures'

const range = '?period=custom&from=2026-09-28&to=2026-09-30'

test('Inspect opens the inspector without filtering, survives reload, resizes, and closes with Esc and Back', async ({ page, owner }) => {
  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}${range}`
  await page.setViewportSize({ width: 1440, height: 1000 })
  await owner.login(page, dashboard)

  const inspect = page.getByRole('region', { name: 'Where' }).getByRole('button', { name: 'Inspect India' })
  await inspect.click()
  await expect.poll(() => new URL(page.url()).searchParams.get('inspect')).toBe('country:IN')
  expect(new URL(page.url()).searchParams.getAll('f')).toEqual([])
  const inspector = page.getByRole('complementary', { name: 'India' })
  await expect(inspector).toBeVisible()
  await expect(inspector.getByRole('heading', { name: 'India' })).toBeFocused()
  await expect(inspector.getByRole('region', { name: 'Metrics for this value' })).toBeVisible()

  const handle = page.getByRole('separator', { name: 'Resize inspector' })
  await handle.focus()
  await page.keyboard.press('ArrowLeft')
  await expect.poll(() => page.evaluate(() => Number(window.localStorage.getItem('supermetry:inspector-width')))).toBeGreaterThan(24)

  await page.reload()
  await expect(page.getByRole('complementary', { name: 'India' })).toBeVisible()
  await page.getByRole('complementary', { name: 'India' }).getByRole('button', { name: 'Close inspector' }).focus()
  await page.keyboard.press('Escape')
  await expect.poll(() => new URL(page.url()).searchParams.get('inspect')).toBeNull()
  await expect(page.getByRole('complementary', { name: 'India' })).toHaveCount(0)

  await page.getByRole('region', { name: 'Where' }).getByRole('button', { name: 'Inspect India' }).click()
  await expect(page.getByRole('complementary', { name: 'India' })).toBeVisible()
  await page.goBack()
  await expect(page.getByRole('complementary', { name: 'India' })).toHaveCount(0)
})

test('the inspector is a sheet below 1280px and a bottom sheet at 390px without horizontal scroll', async ({ page, owner }) => {
  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}${range}&inspect=country%3AIN`
  await page.setViewportSize({ width: 1024, height: 900 })
  await owner.login(page, dashboard)
  await expect(page.getByRole('dialog', { name: 'India' })).toHaveAttribute('data-side', 'right')

  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.getByRole('dialog', { name: 'India' })).toHaveAttribute('data-side', 'bottom')
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth))
    .toBeLessThanOrEqual(0)
  await page.keyboard.press('Escape')
  await expect.poll(() => new URL(page.url()).searchParams.get('inspect')).toBeNull()
})
