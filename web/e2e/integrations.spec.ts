import { execFileSync } from 'node:child_process'
import { mkdirSync } from 'node:fs'
import type { Page } from '@playwright/test'
import { expect, test, type Owner } from './fixtures'

const shots = 'e2e/screenshots/integrations'
const settings = (owner: Owner) => `/app/${owner.orgSlug}/${owner.siteSlug}/settings/integrations`

function syntheticSQL(owner: Owner, sql: string) {
  if (!/^supermetry_e2e_\d+$/.test(owner.schema)) throw new Error('Expected an isolated synthetic schema')
  const dsn = new URL(process.env.SUPERMETRY_TEST_DSN!)
  dsn.searchParams.set('options', `-csearch_path=${owner.schema}`)
  return execFileSync('psql', [dsn.toString(), '-At', '-v', 'ON_ERROR_STOP=1', '-c', sql], { stdio: ['ignore', 'pipe', 'pipe'] }).toString().trim()
}

function clearIntegration(owner: Owner) {
  syntheticSQL(owner, `delete from integration_connections where site_id=${owner.siteId}; delete from search_console_rows where site_id=${owner.siteId};`)
}

function seedConnection(owner: Owner) {
  clearIntegration(owner)
  syntheticSQL(owner, `
    insert into integration_connections(org_id,site_id,provider,property,status,credentials,synced_through,backfill_from,last_sync_at,next_attempt_at)
      values (${owner.orgId},${owner.siteId},'search-console','sc-domain:example.test','active','\\x01',current_date-2,current_date-28,now(),now()+interval '1 day');
    insert into search_console_rows(site_id,day,row_key,query,page,country,device,clicks,impressions,ctr,position)
      select ${owner.siteId}, current_date-3, decode(md5(q||p),'hex'), q, p, 'usa', 'DESKTOP', c, i, c::real/i, pos
      from (values
        ('privacy analytics','https://example.test/privacy',40,800,3.2::real),
        ('web analytics tool','https://example.test/tool',25,1500,7.8::real),
        ('self hosted analytics','https://example.test/self-hosted',12,300,2.1::real)
      ) as v(q,p,c,i,pos);`)
}

async function noHorizontalScroll(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
}

test('connect flow sends the browser to Google, the result is reported, then the report and disconnect work', async ({ page, owner }) => {
  mkdirSync(shots, { recursive: true })
  clearIntegration(owner)
  await owner.login(page, settings(owner))
  await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1)
  await expect(page.getByRole('heading', { level: 1, name: 'Integrations' })).toBeVisible()
  await expect(page.getByRole('navigation', { name: 'Site settings' }).getByRole('link', { name: 'Integrations' })).toHaveAttribute('aria-current', 'page')
  await expect(page.getByText('Not connected')).toBeVisible()

  // Only the Google side is mocked: the browser must be sent to the authorization URL.
  let authorization: URL | undefined
  await page.route('https://accounts.google.com/**', async (route) => {
    authorization = new URL(route.request().url())
    await route.fulfill({ status: 200, contentType: 'text/html', body: '<!doctype html><title>Google sign-in</title><p>mock</p>' })
  })
  await page.getByRole('button', { name: 'Connect with Google' }).click()
  await expect(page.getByText('Enter the Search Console property to import.')).toBeVisible()
  await page.getByLabel('Search Console property').fill('sc-domain:example.test')
  await page.getByLabel('Search Console property').press('Enter')
  await page.waitForURL(/accounts\.google\.com/)
  expect(authorization?.searchParams.get('client_id')).toBe('e2e-client-id.apps.googleusercontent.test')
  expect(authorization?.searchParams.get('code_challenge_method')).toBe('S256')
  expect(authorization?.searchParams.get('state')).toBeTruthy()
  expect(authorization?.searchParams.get('redirect_uri')).toContain('/api/private/integrations/search-console/callback')

  // The callback outcome arrives as query parameters on the real settings route.
  await page.goto(`${settings(owner)}?integration=search-console&status=denied`)
  await expect(page.getByRole('alert').filter({ hasText: 'Access was not granted' })).toBeVisible()
  await page.getByRole('button', { name: 'Dismiss' }).click()
  await expect(page).toHaveURL(new RegExp(`${settings(owner)}$`))

  seedConnection(owner)
  await page.goto(`${settings(owner)}?integration=search-console&status=connected`)
  await expect(page.getByRole('status').filter({ hasText: 'Google Search Console is connected' })).toBeVisible()
  await expect(page.getByText('sc-domain:example.test')).toBeVisible()
  await expect(page.getByText(/^Status: Connected$/)).toBeVisible()
  await expect(page.getByText('Synced through')).toBeVisible()

  const table = page.getByRole('table', { name: /Top queries from/ })
  await expect(table).toBeVisible()
  const firstColumn = () => table.locator('tbody tr td:first-child').allTextContents()
  expect(await firstColumn()).toEqual(['privacy analytics', 'web analytics tool', 'self hosted analytics'])
  await table.getByRole('button', { name: /Avg\. position/ }).click()
  expect(await firstColumn()).toEqual(['self hosted analytics', 'privacy analytics', 'web analytics tool'])
  await expect(table.getByRole('columnheader', { name: /Avg\. position/ })).toHaveAttribute('aria-sort', 'ascending')
  await page.getByLabel('Filter rows').fill('self')
  expect(await firstColumn()).toEqual(['self hosted analytics'])
  await page.getByLabel('Filter rows').fill('')
  await page.getByRole('button', { name: 'Top pages' }).click()
  await expect(page.getByRole('table', { name: /Top pages from/ }).getByText('https://example.test/privacy')).toBeVisible()
  await page.getByRole('button', { name: 'Top queries' }).click()
  await expect(table).toBeVisible()

  for (const [name, width, height, scheme] of [['1440-light', 1440, 1000, 'light'], ['1440-dark', 1440, 1000, 'dark'], ['1024', 1024, 900, 'light'], ['390', 390, 900, 'light']] as const) {
    const dark = await page.evaluate(() => document.documentElement.classList.contains('dark'))
    if ((scheme === 'dark') !== dark) {
      await page.getByRole('button', { name: scheme === 'dark' ? 'Use dark theme' : 'Use light theme' }).click()
    }
    await page.setViewportSize({ width, height })
    await noHorizontalScroll(page)
    await page.screenshot({ path: `${shots}/${name}.png`, fullPage: true })
  }
  await page.setViewportSize({ width: 1280, height: 900 })
  await expect(table.getByRole('cell', { name: '5%' })).toBeVisible()

  // Keyboard: open the confirmation, cancel with Escape, then confirm.
  const disconnect = page.getByRole('button', { name: /^Disconnect/ })
  await disconnect.focus()
  await page.keyboard.press('Enter')
  const dialog = page.getByRole('alertdialog')
  await expect(dialog).toContainText('deletes the stored access tokens and every imported Search Console row')
  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
  expect(syntheticSQL(owner, `select count(*) from search_console_rows where site_id=${owner.siteId}`)).toBe('3')
  await disconnect.click()
  await page.getByRole('button', { name: 'Delete tokens and imported data' }).click()
  await expect(page.getByText('Not connected')).toBeVisible()
  expect(syntheticSQL(owner, `select (select count(*) from search_console_rows where site_id=${owner.siteId}) + (select count(*) from integration_connections where site_id=${owner.siteId})`)).toBe('0')
})

test('interactive targets are at least 44px tall at phone width', async ({ page, owner }) => {
  await page.setViewportSize({ width: 390, height: 900 })
  clearIntegration(owner)
  await owner.login(page, settings(owner))
  await expect(page.getByRole('button', { name: 'Connect with Google' })).toBeVisible()
  for (const locator of [page.getByRole('button', { name: 'Connect with Google' }), page.getByLabel('Search Console property'), page.getByLabel('Import history')]) {
    const box = await locator.boundingBox()
    expect(box!.height).toBeGreaterThanOrEqual(44)
  }
  await noHorizontalScroll(page)
})
