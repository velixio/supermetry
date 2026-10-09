import { execFileSync } from 'node:child_process'

import { expect, test, type Owner } from './fixtures'

const createdAt = '2026-10-01T09:15:00.000Z'
const lastSeenAt = new Date(Date.now() - 24 * 60 * 60 * 1000).toISOString()
const legacyCreatedAt = '2026-09-01T00:00:00.000Z'
const legacyLastSeenAt = new Date(Date.now() - 2 * 24 * 60 * 60 * 1000).toISOString()
const syntheticIP = '2001:db8:1234:5678:abcd:ef01:2345:6789'

function seedSessions(owner: Owner) {
  if (!/^supermetry_e2e_\d+$/.test(owner.schema)) throw new Error('isolated e2e schema required')
  const dsn = process.env.SUPERMETRY_TEST_DSN
  if (!dsn) throw new Error('SUPERMETRY_TEST_DSN is required')
  execFileSync('psql', [dsn, '-v', 'ON_ERROR_STOP=1', '-v', `schema=${owner.schema}`, '-v', `email=${owner.email}`], {
    input: `SET search_path TO :"schema";
      INSERT INTO user_sessions(id_hash,user_id,created_at,last_seen_at,expires_at,device_label,ip_address,country_code,browser)
      SELECT decode(repeat('ab',32),'hex'),id,'${createdAt}','${lastSeenAt}',now()+interval '1 day','Synthetic Firefox','${syntheticIP}','DE','Firefox'
      FROM users WHERE email=:'email';
      INSERT INTO user_sessions(id_hash,user_id,created_at,last_seen_at,expires_at,device_label)
      SELECT decode(repeat('cd',32),'hex'),id,'${legacyCreatedAt}','${legacyLastSeenAt}',now()+interval '1 day','Legacy session'
      FROM users WHERE email=:'email';`,
    stdio: ['pipe', 'pipe', 'pipe'],
  })
}

test('account sessions show real sign-in metadata, legacy fallbacks, local times and mobile revocation', async ({ page, owner }) => {
  await owner.login(page, '/account/sessions')
  const response = await page.request.get('/api/private/me/sessions')
  expect(response.ok()).toBe(true)
  const { data } = await response.json()
  const current = data.find((session: { Current: boolean }) => session.Current)
  expect(current).toMatchObject({ IPAddress: '127.0.0.1', CountryCode: '', Browser: 'Chrome' })
  expect(Number.isFinite(Date.parse(current.CreatedAt))).toBe(true)
  expect(Date.parse(current.LastSeenAt)).toBeGreaterThanOrEqual(Date.parse(current.CreatedAt))

  seedSessions(owner)
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Active sessions', exact: true })).toBeVisible()
  const list = page.getByRole('list', { name: 'Active account sessions' })
  const currentRow = list.getByRole('listitem').filter({ hasText: 'Current session' })
  await expect(list.getByRole('listitem').first()).toContainText('Current session')
  await expect(currentRow.getByText('127.0.0.1', { exact: true })).toBeVisible()
  await expect(currentRow.getByText('Chrome', { exact: true })).toBeVisible()
  await expect(currentRow.locator('img[src^="/icons/"]')).toHaveCount(1)
  await expect(currentRow.getByText('Unknown', { exact: true })).toHaveCount(1)

  const synthetic = list.getByRole('listitem').filter({ hasText: syntheticIP })
  await expect(synthetic.getByText('Germany (DE)', { exact: true })).toBeVisible()
  await expect(synthetic.locator('img[src^="/flags/"]')).toHaveCount(1)
  for (const instant of [createdAt, lastSeenAt]) {
    const time = synthetic.locator(`time[datetime="${instant}"]`)
    await expect(time).toBeVisible()
    await expect(time).toHaveAttribute('title', new RegExp(instant.replaceAll('.', '\\.')))
    const local = await page.evaluate(value => new Date(value).toLocaleString(undefined, {
      year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', second: '2-digit', timeZoneName: 'short',
    }), instant)
    await expect(time).toHaveText(local)
    await expect(time).toHaveAttribute('aria-label', `${local} (${instant})`)
  }
  const legacy = list.getByRole('listitem').filter({ has: page.locator(`time[datetime="${legacyCreatedAt}"]`) })
  await expect(legacy).toHaveCount(1)
  await expect(legacy.getByText('Unknown browser', { exact: true })).toBeVisible()
  await expect(legacy.getByText('Unknown', { exact: true })).toHaveCount(2)
  await expect(legacy.locator('time')).toHaveCount(2)

  await page.setViewportSize({ width: 375, height: 812 })
  await expect(synthetic.getByRole('button', { name: `Revoke Firefox session at ${syntheticIP}` })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  const revoked = page.waitForResponse(response => response.request().method() === 'DELETE' && response.url().includes('/api/private/me/sessions/'))
  await synthetic.getByRole('button', { name: `Revoke Firefox session at ${syntheticIP}` }).click()
  expect((await revoked).ok()).toBe(true)
  await expect(synthetic).toHaveCount(0)
  await expect(currentRow).toHaveCount(1)
  const after = await page.request.get('/api/private/me/sessions')
  expect(after.ok()).toBe(true)
  expect((await after.json()).data.find((session: { ID: string }) => session.ID === current.ID)?.Current).toBe(true)
  expect((await page.request.get('/api/private/me')).ok()).toBe(true)

  const bulkButton = page.getByRole('button', { name: 'Sign out all other devices' })
  await expect(bulkButton).toBeEnabled()
  await bulkButton.click()
  await expect(page.getByRole('heading', { name: 'Sign out all other devices?' })).toBeVisible()
  await expect(page.getByText(/^This signs out \d+ other login sessions? on your account\. This session stays signed in\.$/)).toBeVisible()
  await page.getByRole('button', { name: 'Cancel' }).click()
  await expect(legacy).toHaveCount(1)

  await bulkButton.click()
  const bulkRevoked = page.waitForResponse(response => response.request().method() === 'DELETE' && new URL(response.url()).pathname === '/api/private/me/sessions')
  await page.getByRole('button', { name: 'Sign out other sessions' }).click()
  expect((await bulkRevoked).ok()).toBe(true)
  await expect(legacy).toHaveCount(0)
  await expect(currentRow).toHaveCount(1)
  await expect(bulkButton).toBeDisabled()
  expect((await page.request.get('/api/private/me')).ok()).toBe(true)

  const currentRevoked = page.waitForResponse(response => response.request().method() === 'DELETE' && response.url().includes('/api/private/me/sessions/'))
  await currentRow.getByRole('button', { name: 'Revoke current session', exact: true }).click()
  expect((await currentRevoked).ok()).toBe(true)
  await expect(page).toHaveURL(url => url.pathname === '/login')
  await expect(page.getByRole('heading', { name: 'Sign in to Supermetry', exact: true })).toBeVisible()
  expect((await page.request.get('/api/private/me')).status()).toBe(401)
  await expect(page.getByRole('list', { name: 'Active account sessions' })).toHaveCount(0)
})
