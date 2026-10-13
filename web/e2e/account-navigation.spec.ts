import { execFileSync } from 'node:child_process'
import { expect, test, type Owner } from './fixtures'
import type { APIRequestContext, Page } from '@playwright/test'

const password = 'account-navigation-test-password'
async function member(request: APIRequestContext, owner: Owner, role: 'viewer' | 'editor' | 'admin', suffix: string, noSites = false) {
  const email = `${suffix}@supermetry.test`
  const invitation = await request.post(`/api/private/orgs/${owner.orgId}/invitations`, {
    headers: owner.apiHeaders,
    data: { email, role, ...(role !== 'admin' ? { site_ids: noSites ? [] : [owner.siteId] } : {}) },
  })
  expect(invitation.ok(), await invitation.text()).toBe(true)
  const token = new URL((await invitation.json()).link).searchParams.get('token')
  expect(token).toBeTruthy()
  const accepted = await request.post(`/api/private/invitations/${token}/accept`, { data: { name: suffix, password } })
  expect(accepted.ok(), await accepted.text()).toBe(true)
  return { email, id: (await accepted.json()).user_id as number }
}
async function login(page: Page, email: string) {
  await page.goto('/login')
  await page.getByLabel('Email', { exact: true }).fill(email)
  await page.getByLabel('Password', { exact: true }).fill(password)
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await expect(page).toHaveURL(url => url.pathname !== '/' && !url.pathname.startsWith('/login'))
  await expect(page.getByRole('button', { name: 'Account', exact: true })).toBeVisible()
}
async function logout(page: Page) {
  await page.getByRole('button', { name: 'Account', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Log out', exact: true }).click()
}

test('sidebar settings links and account menu work on desktop and mobile with keyboard focus', async ({ page, owner }) => {
  await owner.login(page, `/app/${owner.orgSlug}/${owner.siteSlug}`)
  const header = page.getByRole('banner', { name: 'Application header' })
  await expect(header.getByRole('button', { name: 'Settings', exact: true })).toHaveCount(0)
  await expect(header.getByRole('button', { name: 'Account', exact: true })).toHaveCount(0)
  for (const width of [1280, 375]) {
    await page.setViewportSize({ width, height: 800 })
    if (width < 768) await page.getByRole('button', { name: 'Toggle sidebar' }).click()
    const sidebar = width < 768
      ? page.getByRole('dialog', { name: 'Sidebar' })
      : page.getByRole('complementary', { name: 'Application sidebar' })
    const settings = sidebar.getByRole('navigation', { name: 'Settings', exact: true })
    await expect(settings.getByRole('link', { name: 'Organization settings', exact: true })).toHaveAttribute('href', `/app/${owner.orgSlug}/settings`)
    await expect(settings.getByRole('link', { name: 'Site settings', exact: true })).toHaveAttribute('href', `/app/${owner.orgSlug}/${owner.siteSlug}/settings/general`)
    const account = sidebar.getByRole('button', { name: 'Account', exact: true })
    await expect(account).toBeInViewport()
    await account.focus()
    await page.keyboard.press('ArrowDown')
    await expect(page.getByText(owner.email, { exact: true }).last()).toBeVisible()
    await expect(page.getByRole('menuitem', { name: 'Log out', exact: true })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(account).toBeFocused()
    if (width < 768) await page.keyboard.press('Escape')
  }
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.getByRole('button', { name: 'Account', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Account settings', exact: true }).click()
  await expect(page).toHaveURL('/app/account/profile')
  await expect(page.getByRole('navigation', { name: 'Account settings' })).toBeVisible()
  // Account pages render inside the same shell: one 44px header with breadcrumbs, the sidebar and one main.
  const accountHeader = page.getByRole('banner', { name: 'Application header' })
  expect((await accountHeader.boundingBox())?.height).toBe(44)
  await expect(accountHeader.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: 'Account', exact: true })).toHaveAttribute('href', '/app/account')
  await expect(page.getByRole('main')).toHaveCount(1)
  const accountLinks = page.getByRole('complementary', { name: 'Application sidebar' }).getByRole('navigation', { name: 'Account', exact: true })
  await expect(accountLinks.getByRole('link', { name: 'Account settings', exact: true })).toHaveAttribute('aria-current', 'page')
  await page.getByRole('link', { name: 'Security', exact: true }).click()
  await expect(page).toHaveURL('/app/account/security')
  await page.screenshot({ path: 'e2e/screenshots/account-mobile.png', fullPage: true })
})

for (const role of ['viewer', 'editor', 'admin'] as const) {
  test(`${role} sees correctly scoped settings and account controls`, async ({ page, owner }) => {
    const user = await member(page.request, owner, role, `header-${role}`)
    await login(page, user.email)
    const settings = page.getByRole('navigation', { name: 'Settings', exact: true })
    await expect(settings.getByRole('link', { name: 'Organization settings', exact: true })).toBeVisible()
    await expect(settings.getByRole('link', { name: 'Site settings', exact: true })).toHaveCount(role === 'viewer' ? 0 : 1)
    await expect(page.getByRole('button', { name: 'Account', exact: true })).toBeVisible()
  })
}

test('no-site and no-organization users can use account pages and log out', async ({ page, owner }) => {
  const user = await member(page.request, owner, 'viewer', 'no-org-account', true)
  await login(page, user.email)
  const settings = page.getByRole('navigation', { name: 'Settings', exact: true })
  await expect(settings.getByRole('link', { name: 'Organization settings', exact: true })).toBeVisible()
  await expect(settings.getByRole('link', { name: 'Site settings', exact: true })).toHaveCount(0)
  if (!/^supermetry_e2e_\d+$/.test(owner.schema)) throw new Error('isolated schema required')
  execFileSync('psql', [process.env.SUPERMETRY_TEST_DSN!, '-v', 'ON_ERROR_STOP=1', '-v', `schema=${owner.schema}`, '-v', `member=${user.id}`], {
    input: 'SET search_path TO :"schema"; DELETE FROM memberships WHERE user_id=:member;',
    stdio: ['pipe', 'pipe', 'pipe'],
  })
  await page.goto('/')
  await expect(page.getByRole('heading', { name: "You don't belong to an organization" })).toBeVisible()
  await expect(page.getByRole('banner', { name: 'Application header' })).toBeVisible()
  await expect(page.getByRole('main')).toHaveCount(1)
  await expect(page.getByRole('navigation', { name: 'Account', exact: true }).getByRole('link', { name: 'Account settings', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Settings', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Account', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Account settings', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Profile', exact: true })).toBeVisible()
  await expect(page.getByLabel('Display name', { exact: true })).toHaveValue('no-org-account')
  await logout(page)
  await expect(page).toHaveURL('/login')
})

test('logout clears the current session across tabs and back navigation; next login has fresh identity', async ({ page, owner }) => {
  const current = await member(page.request, owner, 'admin', 'current-account')
  const next = await member(page.request, owner, 'viewer', 'next-account', true)
  await login(page, current.email)
  await page.goto('/app/account/profile')
  await expect(page).toHaveURL('/app/account/profile')
  await page.evaluate(() => localStorage.setItem('theme', 'dark'))
  const other = await page.context().newPage()
  await other.goto(`/app/${owner.orgSlug}/${owner.siteSlug}`)
  await expect(other.getByRole('button', { name: 'Account', exact: true })).toBeVisible()
  let posts = 0
  page.on('request', request => { if (request.url().endsWith('/auth/logout')) posts++ })
  await logout(page)
  await expect(page).toHaveURL('/login')
  await expect(other).toHaveURL('/login')
  expect(posts).toBe(1)
  expect((await page.request.get('/api/private/me')).status()).toBe(401)
  expect(await page.evaluate(() => localStorage.getItem('theme'))).toBe('dark')
  await page.goBack()
  await expect(page.getByRole('heading', { name: 'Sign in to Supermetry' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Account', exact: true })).toHaveCount(0)
  await login(page, next.email)
  await page.getByRole('button', { name: 'Account', exact: true }).click()
  await expect(page.getByText(next.email, { exact: true }).last()).toBeVisible()
  await expect(page.getByText(current.email, { exact: true })).toHaveCount(0)
  await other.close()
})

for (const failure of ['network', 'server', 'csrf'] as const) {
  test(`logout ${failure} failure is honest and retry succeeds`, async ({ page, owner }) => {
    const user = await member(page.request, owner, 'admin', `logout-${failure}`)
    await login(page, user.email)
    let attempts = 0
    await page.route('**/api/private/auth/logout', async route => {
      attempts++
      expect(route.request().headers()['x-csrf-token']).toBeTruthy()
      if (attempts > 1) return route.continue()
      if (failure === 'network') return route.abort('failed')
      return route.fulfill({ status: failure === 'csrf' ? 403 : 503, body: '{}' })
    })
    await logout(page)
    await expect(page.getByRole('heading', { name: 'Log out could not be confirmed' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Account', exact: true })).toHaveCount(0)
    expect((await page.request.get('/api/private/me')).ok()).toBe(true)
    await page.getByRole('button', { name: 'Retry log out' }).click()
    await expect(page).toHaveURL('/login')
    expect(attempts).toBe(2)
  })
}

test('logout handles an already expired session', async ({ page, owner }) => {
  const user = await member(page.request, owner, 'admin', 'expired-session')
  await login(page, user.email)
  await page.goto('/app/account/profile')
  await expect(page.getByLabel('Display name', { exact: true })).toHaveValue('expired-session')
  await page.getByRole('button', { name: 'Account', exact: true }).click()
  await page.context().clearCookies({ name: 'supermetry_session' })
  await page.getByRole('menuitem', { name: 'Log out', exact: true }).click()
  await expect(page).toHaveURL('/login')
  await expect(page.getByRole('button', { name: 'Account', exact: true })).toHaveCount(0)
})
