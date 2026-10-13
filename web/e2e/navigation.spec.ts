import type { Request } from '@playwright/test'

import { expect, test } from './fixtures'

test('keeps the app shell mounted across links, history, and lazy routes', async ({
  page,
  owner,
}) => {
  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}`
  const funnels = `${dashboard}/funnels`
  const realtime = `${dashboard}/realtime`
  let meRequests = 0
  page.on('request', (request) => {
    if (request.method() === 'GET' && request.url().endsWith('/api/private/me')) meRequests += 1
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  await owner.login(page, dashboard)
  await expect(page.getByRole('navigation', { name: 'Projects' })).toHaveCount(0)
  expect((await page.getByRole('banner', { name: 'Application header' }).boundingBox())?.height).toBe(44)

  let documentRequests = 0
  const countDocuments = (request: Request) => {
    if (request.resourceType() === 'document') documentRequests += 1
  }
  page.on('request', countDocuments)

  const shell = page.locator('[data-slot="sidebar-wrapper"]')
  await shell.evaluate((element) => {
    ;(element as HTMLElement & { supermetryShellToken?: string }).supermetryShellToken = 'persistent'
  })
  await page.getByRole('button', { name: 'Toggle sidebar' }).click()
  await expect(page.locator('[data-slot="sidebar"][data-state="collapsed"]')).toBeVisible()

  await page.getByRole('link', { name: 'Funnels' }).click()
  await expect(page).toHaveURL(funnels)
  await expect(page.getByRole('heading', { name: 'Funnels', exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Funnels' })).toHaveAttribute(
    'aria-current',
    'page',
  )
  expect(documentRequests).toBe(0)
  expect(
    await shell.evaluate(
      (element) =>
        (element as HTMLElement & { supermetryShellToken?: string }).supermetryShellToken,
    ),
  ).toBe('persistent')
  await expect(page.locator('[data-slot="sidebar"][data-state="collapsed"]')).toBeVisible()

  await page.goBack()
  await expect(page).toHaveURL((url) => url.pathname === dashboard)
  await expect(page.getByRole('link', { name: 'Dashboard' })).toHaveAttribute(
    'aria-current',
    'page',
  )
  await page.goForward()
  await expect(page).toHaveURL(funnels)
  await expect(page.getByRole('heading', { name: 'Funnels', exact: true })).toBeVisible()
  expect(documentRequests).toBe(0)
  expect(meRequests).toBe(1)

  await page.route('**/assets/*.js', async (route) => {
    if (route.request().url().includes('realtime')) {
      await new Promise((resolve) => setTimeout(resolve, 700))
    }
    await route.continue()
  })
  await page.evaluate((href) => {
    const link = document.querySelector<HTMLAnchorElement>(`a[href="${href}"]`)
    if (!link) throw new Error(`missing navigation link: ${href}`)
    link.click()
  }, realtime)

  await expect(page.getByRole('status', { name: 'Loading page' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Toggle sidebar' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Realtime' })).toBeVisible()
  await expect(page.getByRole('status', { name: 'Loading page' })).toHaveCount(0)
  expect(documentRequests).toBe(0)
})

test('keeps the same shell mounted between a site and the account pages', async ({ page, owner }) => {
  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}`
  await page.setViewportSize({ width: 1440, height: 900 })
  await owner.login(page, dashboard)
  const shell = page.locator('[data-slot="sidebar-wrapper"]')
  await shell.evaluate((element) => {
    ;(element as HTMLElement & { supermetryShellToken?: string }).supermetryShellToken = 'persistent'
  })
  let documentRequests = 0
  page.on('request', (request) => {
    if (request.resourceType() === 'document') documentRequests += 1
  })
  await page.getByRole('button', { name: 'Account', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Account settings', exact: true }).click()
  await expect(page).toHaveURL('/app/account/profile')
  expect((await page.getByRole('banner', { name: 'Application header' }).boundingBox())?.height).toBe(44)
  await expect(page.getByRole('main')).toHaveCount(1)
  const token = () => shell.evaluate((element) => (element as HTMLElement & { supermetryShellToken?: string }).supermetryShellToken)
  expect(await token()).toBe('persistent')
  await page.goBack()
  await expect(page).toHaveURL((url) => url.pathname === dashboard)
  expect(await token()).toBe('persistent')
  expect(documentRequests).toBe(0)
})

test('serves an authenticated nested route as a direct URL', async ({ page, owner }) => {
  const settings = `/app/${owner.orgSlug}/${owner.siteSlug}/settings/general`
  await owner.login(page, `/app/${owner.orgSlug}/${owner.siteSlug}`)
  const response = await page.goto(settings)

  expect(response?.ok()).toBe(true)
  await expect(page).toHaveURL(settings)
  await expect(page.getByRole('heading', { name: 'Site settings' })).toBeVisible()
  await expect(page.getByRole('link', { name: 'General', exact: true })).toHaveAttribute(
    'aria-current',
    'page',
  )
})

test('renders auth screens dark without leaking into a light dashboard', async ({
  page,
  owner,
}) => {
  await page.addInitScript(() => localStorage.setItem('theme', 'light'))

  await page.goto('/login')
  await expect(page.locator('html')).toHaveClass(/dark/)
  await expect(page.getByRole('main')).toHaveClass(/dark/)

  await page.goto('/signup')
  await expect(page.locator('html')).toHaveClass(/dark/)
  await expect(page.getByRole('main')).toHaveClass(/dark/)

  await owner.login(page, `/app/${owner.orgSlug}/${owner.siteSlug}`)
  await expect(page.locator('html')).not.toHaveClass(/dark/)
  await expect(page.locator('meta[name="theme-color"]')).toHaveAttribute(
    'content',
    '#fafafa',
  )
})

test('starts on the icon rail at tablet width and keeps the saved sidebar state', async ({ page, owner }) => {
  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}`
  await page.setViewportSize({ width: 1024, height: 800 })
  await owner.login(page, dashboard)
  const sidebar = page.locator('[data-slot="sidebar"]')
  await expect(sidebar).toHaveAttribute('data-state', 'collapsed')
  await expect(page.getByRole('link', { name: 'Funnels' })).toBeVisible()
  await page.getByRole('button', { name: 'Toggle sidebar' }).click()
  await expect(sidebar).toHaveAttribute('data-state', 'expanded')
  await page.reload()
  await expect(sidebar).toHaveAttribute('data-state', 'expanded')
  expect((await page.getByRole('banner', { name: 'Application header' }).boundingBox())?.height).toBe(44)
})

test('opens the command palette with Ctrl+K and from the header, and navigates without a reload', async ({ page, owner }) => {
  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}`
  await page.setViewportSize({ width: 1440, height: 900 })
  await owner.login(page, dashboard)
  let documentRequests = 0
  page.on('request', (request) => {
    if (request.resourceType() === 'document') documentRequests += 1
  })
  const trigger = page.getByRole('banner', { name: 'Application header' }).getByRole('button', { name: 'Open command palette' })
  await expect(trigger).toBeVisible()
  await page.keyboard.press('Control+KeyK')
  const palette = page.getByRole('dialog', { name: 'Command palette' })
  await expect(palette).toBeVisible()
  await palette.getByRole('combobox', { name: 'Search commands' }).fill('Funnels')
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(`${dashboard}/funnels`)
  await expect(palette).toHaveCount(0)
  expect(documentRequests).toBe(0)

  await trigger.click()
  await expect(palette).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(palette).toHaveCount(0)
  await expect(trigger).toBeFocused()
})
