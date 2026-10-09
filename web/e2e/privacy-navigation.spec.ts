import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import type { Page } from '@playwright/test'
import { expect, test, type Owner } from './fixtures'


const routes = [
  'events', 'sessions', 'profiles', 'profiles/999', 'journeys', 'heatmaps',
  'replays', 'replays/999', 'funnels', 'funnels/999', 'vitals', 'retention',
  'goals', 'realtime', 'EVENTS', 'Replays/999', '%65vents',
] as const
const historyAPI = /\/api\/private\/(?:sites\/\d+\/)?(?:events|properties|sessions|profiles|journeys|heatmaps|replays|funnels|vitals|retention|goals|realtime)(?:\/|\?|$)/
const restriction = (page: Page) => page.getByRole('status', { name: 'Tracking privacy restriction' })
const siteRoot = (owner: Owner) => `/${owner.orgSlug}/${owner.siteSlug}`

function watchPrivacyRequests(page: Page) {
  const forbidden: string[] = []
  const history: string[] = []
  page.on('response', (response) => {
    if (response.status() === 403 && response.url().includes('/api/')) forbidden.push(response.url())
  })
  page.on('request', (request) => {
    if (historyAPI.test(request.url()) && !request.url().includes('/realtime/summary')) history.push(request.url())
  })
  return { forbidden, history }
}

test('anonymous direct routes show policy restrictions without forbidden requests or polling', async ({ page, owner }) => {
  test.skip(process.env.PANEL_TRACKING_MODE !== 'anonymous', 'Requires the real anonymous instance gate.')
  await owner.login(page, `${siteRoot(owner)}/settings/general`)
  const seen = watchPrivacyRequests(page)
  await page.goto(`${siteRoot(owner)}/`)
  await expect(page.getByRole('region', { name: 'Verified pages', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Visitors 0', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Pageviews 0', exact: true })).toBeVisible()
  for (const route of routes) {
    await test.step(route, async () => {
      await page.goto(`${siteRoot(owner)}/${route}`)
      await expect(restriction(page)).toBeVisible()
      await expect(page.getByRole('heading', { name: 'Unavailable in anonymous tracking mode' })).toBeVisible()
      await expect(page.getByText(/request failed|forbidden|403/i)).toHaveCount(0)
    })
  }
  await page.clock.install()
  await page.clock.fastForward(65_000)
  expect(seen.forbidden).toEqual([])
  expect(seen.history).toEqual([])
})

function syntheticSQL(owner: Owner, sql: string) {
  if (!/^panel_e2e_\d+$/.test(owner.schema)) throw new Error('Expected an isolated synthetic schema')
  const dsn = new URL(process.env.PANEL_TEST_DSN!)
  dsn.searchParams.set('options', `-csearch_path=${owner.schema}`)
  return execFileSync('psql', [dsn.toString(), '-At', '-v', 'ON_ERROR_STOP=1', '-c', sql], { stdio: ['ignore', 'pipe', 'pipe'] }).toString().trim()
}

const legacyMarker = 'legacy-private-person-927'
function seedLegacyAndVerifiedRows(owner: Owner) {
  const policy = createHash('sha256').update('panel/path-sanitizer/v1\n').digest('hex')
  syntheticSQL(owner, `
    insert into dict(kind,value) values (0,'/${legacyMarker}'),(0,'/[redacted]'),(9,'Chrome') on conflict do nothing;
    insert into profiles(site_id,external_id,traits,first_seen_at,last_seen_at)
      values (${owner.siteId},'${legacyMarker}','{"private":"${legacyMarker}"}',now(),now());
    insert into sessions(site_id,retention_class,session_id,visitor_id,start,last,local_day,
      entry_path,exit_path,country,browser_id,device,pageviews,events,bounce)
      values (${owner.siteId},30,92701,92701,now()-interval '10 minutes',now(),(now() at time zone 'America/New_York')::date,
      (select id from dict where kind=0 and value='/${legacyMarker}'),(select id from dict where kind=0 and value='/${legacyMarker}'),
      'US',(select id from dict where kind=9 and value='Chrome'),1,1,0,true),
      (${owner.siteId},30,92702,92702,now()-interval '5 minutes',now(),(now() at time zone 'America/New_York')::date,
      (select id from dict where kind=0 and value='/[redacted]'),(select id from dict where kind=0 and value='/[redacted]'),
      'US',(select id from dict where kind=9 and value='Chrome'),1,1,0,true);
    insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id,country,browser_id,device,props,profile_id)
      values (${owner.siteId},30,(now() at time zone 'America/New_York')::date,now(),92701,92701,0,
      (select id from dict where kind=0 and value='/${legacyMarker}'),'US',(select id from dict where kind=9 and value='Chrome'),1,
      '{"private":"${legacyMarker}"}',(select id from profiles where site_id=${owner.siteId} and external_id='${legacyMarker}'));
    insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id,country,browser_id,device,path_sanitizer_version,path_policy_id)
      values (${owner.siteId},30,(now() at time zone 'America/New_York')::date,now(),92702,92702,0,
      (select id from dict where kind=0 and value='/[redacted]'),'US',(select id from dict where kind=9 and value='Chrome'),1,1,'${policy}');
  `)
}

test('anonymous dashboard keeps canonical totals and labels verified coverage without exposing legacy identity', async ({ page, owner }) => {
  test.skip(process.env.PANEL_TRACKING_MODE !== 'anonymous', 'Requires the real anonymous instance gate.')
  // Warm the live dictionary through the real anonymous ingestion pipeline;
  // inserting dictionary rows only through SQL would leave its cache stale.
  const hit = await page.request.post('/api/public/v1/events', {
    headers: { Origin: 'https://site1.test', 'User-Agent': 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36' },
    data: { site: owner.siteSlug, name: 'pageview', url: 'https://site1.test/unlisted-public-fixture' },
  })
  expect(hit.status()).toBe(202)
  await expect.poll(() => syntheticSQL(owner, "select count(*) from dict where (kind=0 and value='/[redacted]') or (kind=9 and value='Chrome')")).toBe('2')
  seedLegacyAndVerifiedRows(owner)
  // Wait for the ingested session to flush before the first dashboard query is cached.
  await expect.poll(() => syntheticSQL(owner, `select count(*) from sessions where site_id=${owner.siteId}`)).toBe('3')
  await owner.login(page, `${siteRoot(owner)}/settings/general`)
  const seen = watchPrivacyRequests(page)
  const queries: Array<{ dimensions?: string[]; metrics?: string[]; filters?: unknown[] }> = []
  page.on('request', (request) => {
    if (request.url().endsWith('/api/private/query') && request.method() === 'POST') queries.push(request.postDataJSON())
  })
  await page.goto(`${siteRoot(owner)}/`)
  const pages = page.getByRole('region', { name: 'Verified pages', exact: true })
  await expect(pages).toBeVisible()
  await expect(pages.getByText(/Partial coverage: only verified page data/)).toBeVisible()
  await expect(page.getByRole('region', { name: 'Countries', exact: true })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Channels', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Visitors 3', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Pageviews 3', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: /Filter by country United States/ })).toBeVisible()
  await expect(pages.getByText('/[redacted]', { exact: true })).toBeVisible()
  await expect(pages.getByRole('row').filter({ hasText: '/[redacted]' })).toHaveText(/\/\[redacted\]\s*2\s*2/)
  await expect(pages.getByRole('button', { name: /Filter by/ })).toHaveCount(0)
  await expect(page.getByText(legacyMarker, { exact: false })).toHaveCount(0)
  await expect(page.getByText(/request failed|forbidden|403/i)).toHaveCount(0)
  expect(queries.length).toBeGreaterThan(0)
  for (const query of queries) {
    for (const dimension of query.dimensions ?? []) {
      expect(['time', 'hour', 'country', 'browser', 'os', 'device', 'channel', 'page']).toContain(dimension)
    }
  }
  expect(seen.forbidden).toEqual([])
  expect(seen.history).toEqual([])

  await page.getByRole('button', { name: 'Filter by country United States', exact: true }).click()
  await expect(page.getByText(/Verified page analytics require no filters/)).toBeVisible()
  const beforeUnsafeURL = queries.length
  await page.goto(`${siteRoot(owner)}/?f=source%3Ais%3AGoogle`)
  await expect(page.getByRole('heading', { name: 'These filters are unavailable in anonymous mode' })).toBeVisible()
  expect(queries).toHaveLength(beforeUnsafeURL)
  await page.getByRole('button', { name: 'Clear unavailable filters' }).click()
  await expect(pages).toBeVisible()

  // Direct history paths remain blocked even when persisted identity exists.
  for (const route of ['events', 'sessions', 'profiles', 'journeys', 'heatmaps']) {
    await page.goto(`${siteRoot(owner)}/${route}`)
    await expect(restriction(page)).toBeVisible()
    await expect(page.getByText(legacyMarker, { exact: false })).toHaveCount(0)
  }
  expect(seen.forbidden).toEqual([])
  expect(seen.history).toEqual([])
})

test('anonymous mobile navigation and browser history retain policy boundaries', async ({ page, owner }) => {
  test.skip(process.env.PANEL_TRACKING_MODE !== 'anonymous', 'Requires the real anonymous instance gate.')
  await page.setViewportSize({ width: 390, height: 844 })
  await owner.login(page, `${siteRoot(owner)}/events`)
  const seen = watchPrivacyRequests(page)
  await expect(restriction(page)).toBeVisible()
  await page.getByRole('link', { name: /Dashboard/i }).last().click()
  await expect(page.getByRole('region', { name: 'Verified pages', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Toggle sidebar' }).click()
  const menu = page.getByRole('dialog')
  await expect(menu).toBeVisible()
  for (const name of ['Events', 'Sessions', 'Profiles', 'Journeys', 'Heatmaps', 'Replays', 'Funnels', 'Web Vitals', 'Retention', 'Goals', 'Realtime']) {
    await expect(menu.getByRole('link', { name, exact: true })).toHaveCount(0)
  }
  await page.keyboard.press('Escape')
  await page.goBack()
  await expect(restriction(page)).toBeVisible()
  await page.goForward()
  await expect(page.getByRole('region', { name: 'Verified pages', exact: true })).toBeVisible()
  await expect(page.getByText(legacyMarker, { exact: false })).toHaveCount(0)
  expect(seen.forbidden).toEqual([])
  expect(seen.history).toEqual([])
})

// Accounts exist only in the disposable test schema; authentication still uses
// the actual login page and server password verification.
function seedReplayReader(owner: Owner, role: 'admin' | 'viewer') {
  const email = `privacy-navigation-${role}@panel.test`
  syntheticSQL(owner, `
    with account as (
      insert into users(email,name,password_hash)
        select '${email}','Privacy navigation ${role}',password_hash
        from users where id=(select user_id from memberships where org_id=${owner.orgId} and role='owner' limit 1)
      returning id
    ) insert into memberships(org_id,user_id,role)
      select ${owner.orgId},id,'${role}' from account;
  `)
  return email
}

async function loginReplayReader(page: Page, owner: Owner, email: string) {
  await page.context().clearCookies()
  const next = `${siteRoot(owner)}/settings/general`
  await page.goto(`/login?next=${encodeURIComponent(next)}`)
  await page.getByLabel('Email').fill(email)
  await page.getByLabel('Password').fill(owner.password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await page.waitForURL((url) => url.pathname === next)
}

test('identified owners and admins retain replay navigation with profiles off and recording on or off', async ({ page, owner }) => {
  test.skip(process.env.PANEL_TRACKING_MODE !== 'identified' || process.env.PANEL_REPLAY_ENABLED !== 'true', 'Requires real identified mode and instance replay permission.')
  expect(syntheticSQL(owner, `select coalesce(settings->>'profiles_enabled','false') from sites where id=${owner.siteId}`)).toBe('false')
  const admin = seedReplayReader(owner, 'admin')
  const seen = watchPrivacyRequests(page)
  for (const email of [owner.email, admin]) {
    await loginReplayReader(page, owner, email)
    for (const enabled of [true, false]) {
      await test.step(`${email === owner.email ? 'owner' : 'admin'} recording ${enabled ? 'on' : 'off'}`, async () => {
        const changed = await page.request.put(`/api/private/sites/${owner.siteId}/replay-settings`, {
          headers: owner.apiHeaders, data: { enabled },
        })
        expect(changed.ok(), await changed.text()).toBe(true)
        await page.goto(`${siteRoot(owner)}/settings/general`)
        const link = page.getByRole('link', { name: 'Replays', exact: true })
        await expect(link).toBeVisible()
        await link.click()
        await expect(page.getByRole('heading', { name: 'Replays', exact: true })).toBeVisible()
        const recordingStatus = page.getByRole('complementary', { name: 'Recording status' })
        if (enabled) {
          await expect(recordingStatus).toHaveCount(0)
          await expect(page.getByRole('link', { name: 'Replay settings', exact: true })).toHaveCount(0)
        } else {
          await expect(recordingStatus.getByRole('heading', { name: 'Recording is off', exact: true })).toBeVisible()
          await expect(recordingStatus.getByRole('link', { name: 'Replay settings', exact: true })).toBeVisible()
        }
        await expect(page.getByText('No replays found', { exact: true })).toBeVisible()
        await expect(page.getByText('Existing recordings remain available to authorized viewers.', { exact: true })).toHaveCount(enabled ? 0 : 1)
        await expect(page.getByText(/Instance permission: on\. Project opt-in:/)).toHaveCount(enabled ? 0 : 1)
        expect(seen.forbidden).toEqual([])
      })
    }
  }
})

test('identified viewer without replay grant has no replay navigation or history requests', async ({ page, owner }) => {
  test.skip(process.env.PANEL_TRACKING_MODE !== 'identified', 'Requires the real identified instance gate.')
  const viewer = seedReplayReader(owner, 'viewer')
  await loginReplayReader(page, owner, viewer)
  const seen = watchPrivacyRequests(page)
  await expect(page.getByRole('link', { name: 'Replays', exact: true })).toHaveCount(0)
  await page.goto(`${siteRoot(owner)}/replays`)
  await expect(page.getByRole('alert').filter({ hasText: 'You don’t have permission to watch replays' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Replays', exact: true })).toHaveCount(0)
  expect(seen.forbidden).toEqual([])
  expect(seen.history.filter((url) => /\/replays(?:\/|\?|$)/.test(url))).toEqual([])
})
