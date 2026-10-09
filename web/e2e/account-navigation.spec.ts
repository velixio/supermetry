import { execFileSync } from 'node:child_process'
import { expect, test, type Owner } from './fixtures'
import type { APIRequestContext, Page } from '@playwright/test'

const password = 'account-navigation-test-password'
async function member(request: APIRequestContext, owner: Owner, role: 'viewer' | 'editor' | 'admin', suffix: string, noSites = false) {
  const email = `${suffix}@panel.test`
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

test('desktop and mobile header menus preserve settings links and keyboard focus', async ({ page, owner }) => {
  await owner.login(page, `/${owner.orgSlug}/${owner.siteSlug}`)
  await expect(page.getByRole('link', { name: 'Organization settings', exact: true })).toHaveCount(0)
  await expect(page.getByRole('link', { name: 'Site settings', exact: true })).toHaveCount(0)
  for (const width of [1280, 375]) {
    await page.setViewportSize({ width, height: 800 })
    const settings = page.getByRole('button', { name: 'Settings', exact: true })
    await expect(settings).toBeInViewport()
    await settings.focus()
    await page.keyboard.press('ArrowDown')
    await expect(page.getByRole('menuitem', { name: /Organization settings Acme/ })).toHaveAttribute('href', `/${owner.orgSlug}/settings`)
    await expect(page.getByRole('menuitem', { name: /Site settings Site 1/ })).toHaveAttribute('href', `/${owner.orgSlug}/${owner.siteSlug}/settings/general`)
    await page.keyboard.press('Escape')
    await expect(settings).toBeFocused()
    const account = page.getByRole('button', { name: 'Account', exact: true })
    await expect(account).toBeInViewport()
    await account.focus()
    await page.keyboard.press('ArrowDown')
    await expect(page.getByText(owner.email, { exact: true }).last()).toBeVisible()
    await expect(page.getByRole('menuitem', { name: 'Log out', exact: true })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(account).toBeFocused()
  }
  await page.getByRole('button', { name: 'Account', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Account settings', exact: true }).click()
  await expect(page).toHaveURL('/account/profile')
  await expect(page.getByRole('navigation', { name: 'Account settings' })).toBeVisible()
  await page.getByRole('link', { name: 'Security', exact: true }).click()
  await expect(page).toHaveURL('/account/security')
  await page.screenshot({ path: 'e2e/screenshots/account-mobile.png', fullPage: true })
})

for (const role of ['viewer', 'editor', 'admin'] as const) {
  test(`${role} sees correctly scoped settings and account controls`, async ({ page, owner }) => {
    const user = await member(page.request, owner, role, `header-${role}`)
    await login(page, user.email)
    await page.getByRole('button', { name: 'Settings', exact: true }).click()
    await expect(page.getByRole('menuitem', { name: /Organization settings Acme/ })).toBeVisible()
    await expect(page.getByRole('menuitem', { name: /Site settings Site 1/ })).toHaveCount(role === 'viewer' ? 0 : 1)
    await page.keyboard.press('Escape')
    await expect(page.getByRole('button', { name: 'Account', exact: true })).toBeVisible()
  })
}

test('no-site and no-organization users can use account pages and log out', async ({ page, owner }) => {
  const user = await member(page.request, owner, 'viewer', 'no-org-account', true)
  await login(page, user.email)
  await page.getByRole('button', { name: 'Settings', exact: true }).click()
  await expect(page.getByRole('menuitem', { name: /Site settings/ })).toHaveCount(0)
  await page.keyboard.press('Escape')
  if (!/^panel_e2e_\d+$/.test(owner.schema)) throw new Error('isolated schema required')
  execFileSync('psql', [process.env.PANEL_TEST_DSN!, '-v', 'ON_ERROR_STOP=1', '-v', `schema=${owner.schema}`, '-v', `member=${user.id}`], {
    input: 'SET search_path TO :"schema"; DELETE FROM memberships WHERE user_id=:member;',
    stdio: ['pipe', 'pipe', 'pipe'],
  })
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'No organization access' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Settings', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Account', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Account settings', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Profile', exact: true })).toBeVisible()
  await expect(page.getByLabel('Display name', { exact: true })).toHaveValue('no-org-account')
  await logout(page)
  await expect(page).toHaveURL('/login')
})

test('logout clears the current session across tabs and back navigation; next login has fresh identity', async ({ page, owner }) => {
  const next = await member(page.request, owner, 'viewer', 'next-account', true)
  await owner.login(page, `/${owner.orgSlug}/${owner.siteSlug}`)
  await page.getByRole('button', { name: 'Account', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Account settings', exact: true }).click()
  await expect(page).toHaveURL('/account/profile')
  await page.evaluate(() => localStorage.setItem('theme', 'dark'))
  const other = await page.context().newPage()
  await other.goto(`/${owner.orgSlug}/${owner.siteSlug}`)
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
  await expect(page.getByRole('heading', { name: 'Sign in to Panel' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Account', exact: true })).toHaveCount(0)
  await login(page, next.email)
  await page.getByRole('button', { name: 'Account', exact: true }).click()
  await expect(page.getByText(next.email, { exact: true }).last()).toBeVisible()
  await expect(page.getByText(owner.email, { exact: true })).toHaveCount(0)
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
  await page.goto('/account/profile')
  await expect(page.getByLabel('Display name', { exact: true })).toHaveValue('expired-session')
  await page.getByRole('button', { name: 'Account', exact: true }).click()
  await page.context().clearCookies({ name: 'panel_session' })
  await page.getByRole('menuitem', { name: 'Log out', exact: true }).click()
  await expect(page).toHaveURL('/login')
  await expect(page.getByRole('button', { name: 'Account', exact: true })).toHaveCount(0)
})
