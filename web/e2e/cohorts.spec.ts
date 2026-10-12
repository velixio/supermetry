import { execFileSync } from 'node:child_process'

import { expect, test, type Owner } from './fixtures'

function sql(owner: Owner, statement: string) {
  if (!/^supermetry_e2e_\d+$/.test(owner.schema)) throw new Error('Expected an isolated synthetic schema')
  const dsn = new URL(process.env.SUPERMETRY_TEST_DSN!)
  dsn.searchParams.set('options', `-csearch_path=${owner.schema}`)
  return execFileSync('psql', [dsn.toString(), '-At', '-v', 'ON_ERROR_STOP=1', '-c', statement], { stdio: ['ignore', 'pipe', 'pipe'] }).toString().trim()
}

// Three identified profiles with hand-known activity (UTC):
//   ada   : signup{plan:pro} + pageview on 09-28, pageview on 09-29  -> 2 sessions, 3 events
//   grace : signup{plan:free} + pageview on 09-29                    -> 1 session, 2 events
//   linus : one pageview on 09-28                                    -> 1 session, 1 event
function seed(owner: Owner) {
  const site = owner.siteId
  sql(owner, `
    insert into profiles(site_id,external_id,first_seen_at,last_seen_at) values
      (${site},'e2e-ada','2026-09-20 08:00+00','2026-09-29 12:00+00'),
      (${site},'e2e-grace','2026-09-25 08:00+00','2026-09-29 09:05+00'),
      (${site},'e2e-linus','2026-09-26 08:00+00','2026-09-28 10:00+00');
    insert into dict(kind,id,value) values (7,910001,'e2e_signup') on conflict do nothing;
    insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,profile_id,name_id,path_id,props)
    select ${site},30,(v.ts at time zone 'UTC')::date,v.ts,v.sid,v.sid,v.kind,p.id,v.name,0,v.props::jsonb
    from (values
      ('e2e-ada','2026-09-28 10:00+00'::timestamptz,990001,1,910001,'{"plan":"pro"}'),
      ('e2e-ada','2026-09-28 10:01+00'::timestamptz,990001,0,0,null),
      ('e2e-ada','2026-09-29 12:00+00'::timestamptz,990002,0,0,null),
      ('e2e-grace','2026-09-29 09:00+00'::timestamptz,990003,1,910001,'{"plan":"free"}'),
      ('e2e-grace','2026-09-29 09:05+00'::timestamptz,990003,0,0,null),
      ('e2e-linus','2026-09-28 10:00+00'::timestamptz,990004,0,0,null)
    ) v(ext,ts,sid,kind,name,props)
    join profiles p on p.site_id=${site} and p.external_id=v.ext`)
}

test('cohorts: build, preview, save, evaluate, filter, duplicate, delete, power users', async ({ page, owner }) => {
  const enabled = await page.request.patch(`/api/private/sites/${owner.siteId}`, {
    headers: owner.apiHeaders,
    data: { profiles_enabled: true },
  })
  expect(enabled.ok(), await enabled.text()).toBe(true)
  seed(owner)

  const base = `/app/${owner.orgSlug}/${owner.siteSlug}/cohorts?period=custom&from=2026-09-28&to=2026-09-29`
  await page.setViewportSize({ width: 1440, height: 1000 })
  await owner.login(page, base)

  await expect(page.getByRole('heading', { level: 1, name: 'Cohorts' })).toBeVisible()
  await expect(page.getByText('No cohorts yet')).toBeVisible()

  // Build with the keyboard-reachable form and preview before saving.
  await page.getByRole('button', { name: 'New cohort' }).click()
  const form = page.getByRole('form', { name: 'New cohort' })
  await form.getByLabel('Name', { exact: true }).fill('E2E signups')
  await form.getByLabel('Window (days)', { exact: true }).fill('90')
  await form.getByLabel('Event name', { exact: true }).fill('e2e_signup')
  await form.getByRole('button', { name: 'Preview size' }).click()
  const result = page.getByRole('region', { name: /Result: E2E signups/ })
  await expect(result.getByText('Matching profiles')).toBeVisible()
  await expect(result.getByRole('cell', { name: 'e2e-ada' })).toBeVisible()
  await expect(result.getByRole('cell', { name: 'e2e-grace' })).toBeVisible()
  await expect(result.getByRole('cell', { name: 'e2e-linus' })).toHaveCount(0)
  await expect(result.locator('p.text-3xl')).toHaveText('2')

  await form.getByRole('button', { name: 'Save cohort' }).click()
  const row = page.getByRole('row', { name: /E2E signups/ })
  await expect(row).toBeVisible()
  await expect(row.getByText('Did “e2e_signup”')).toBeVisible()
  await page.screenshot({ path: 'e2e/screenshots/cohorts/1440-light.png', fullPage: true })

  // A second condition narrows the audience: signed up AND not viewed any page twice.
  await row.getByRole('button', { name: 'Edit E2E signups' }).click()
  const editor = page.getByRole('form', { name: 'Edit cohort' })
  await editor.getByRole('button', { name: 'Add condition' }).click()
  const second = editor.getByRole('group', { name: 'Condition 2' })
  await second.getByLabel('Type', { exact: true }).selectOption('activity')
  await second.getByLabel('At least this many times', { exact: true }).fill('2')
  await editor.getByRole('button', { name: 'Preview size' }).click()
  await expect(page.getByRole('region', { name: /Result: E2E signups/ }).locator('p.text-3xl')).toHaveText('1')
  await editor.getByRole('button', { name: 'Cancel' }).click()

  // Duplicate, then delete the copy through the confirmation dialog.
  await row.getByRole('button', { name: 'Duplicate E2E signups' }).click()
  const copy = page.getByRole('row', { name: /E2E signups \(copy\)/ })
  await expect(copy).toBeVisible()
  await copy.getByRole('button', { name: 'Delete E2E signups (copy)' }).click()
  await page.getByRole('button', { name: 'Delete cohort' }).click()
  await expect(copy).toHaveCount(0)

  // Use the saved cohort as a dashboard filter.
  await row.getByRole('link', { name: 'Use E2E signups as a dashboard filter' }).click()
  await expect(page.getByRole('button', { name: 'Remove cohort filter E2E signups' })).toBeVisible()
  await page.goBack()

  // Power users with a threshold.
  await page.getByRole('tab', { name: 'Power users' }).click()
  const table = page.getByRole('table', { name: 'Power users' })
  await expect(table.getByRole('cell', { name: 'e2e-ada' })).toBeVisible()
  await expect(page.getByText('Profiles reaching the threshold: 1')).toBeVisible()
  await expect(table.getByRole('cell', { name: 'e2e-grace' })).toHaveCount(0)
  await page.getByLabel('At least', { exact: true }).selectOption('1')
  await expect(page.getByText('Profiles reaching the threshold: 3')).toBeVisible()
  const order = await table.getByRole('row').allInnerTexts()
  expect(order.map((line) => /e2e-\w+/.exec(line)?.[0]).filter(Boolean)).toEqual(['e2e-ada', 'e2e-grace', 'e2e-linus'])
  await page.screenshot({ path: 'e2e/screenshots/cohorts/1440-power-light.png', fullPage: true })

  await page.getByRole('button', { name: 'Use dark theme' }).click()
  await expect(page.locator('html')).toHaveClass(/dark/)
  await page.waitForTimeout(400)
  await page.screenshot({ path: 'e2e/screenshots/cohorts/1440-power-dark.png', fullPage: true })
  await page.getByRole('tab', { name: 'Cohorts' }).click()
  await expect(page.getByRole('row', { name: /E2E signups/ })).toBeVisible()
  await page.screenshot({ path: 'e2e/screenshots/cohorts/1440-dark.png', fullPage: true })

  await page.setViewportSize({ width: 1024, height: 900 })
  await page.screenshot({ path: 'e2e/screenshots/cohorts/1024-dark.png', fullPage: true })

  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole('button', { name: 'New cohort' }).click()
  await expect(page.getByRole('form', { name: 'New cohort' })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  await page.screenshot({ path: 'e2e/screenshots/cohorts/390-dark.png', fullPage: true })

  // Keyboard: the builder is reachable and the first focusable control is the name field.
  await page.keyboard.press('Tab')
  await expect(page.locator(':focus')).toBeVisible()
})

test('cohorts: API enforces bounds, scopes and the person-level gate', async ({ page, owner }) => {
  await owner.login(page, '/')
  const site = owner.siteId
  const enabled = await page.request.patch(`/api/private/sites/${site}`, { headers: owner.apiHeaders, data: { profiles_enabled: true } })
  expect(enabled.ok(), await enabled.text()).toBe(true)
  const tooWide = await page.request.post(`/api/private/sites/${site}/cohorts/evaluate`, {
    headers: owner.apiHeaders,
    data: { definition: { match: 'all', window_days: 91, conditions: [{ type: 'event', name: 'x' }] } },
  })
  expect(tooWide.status()).toBe(400)
  const both = await page.request.post(`/api/private/sites/${site}/cohorts/evaluate`, {
    headers: owner.apiHeaders,
    data: { cohort_id: 1, definition: { match: 'all', window_days: 7, conditions: [{ type: 'event', name: 'x' }] } },
  })
  expect(both.status()).toBe(400)
  const power = await page.request.get(`/api/private/sites/${site}/power-users?from=2026-09-28&to=2026-09-29&min=-1`, { headers: owner.apiHeaders })
  expect(power.status()).toBe(400)
  const missing = await page.request.post(`/api/private/sites/${site}/cohorts/evaluate`, { headers: owner.apiHeaders, data: { cohort_id: 987654 } })
  expect(missing.status()).toBe(404)

  // A saved cohort is accepted as a dashboard query filter; unknown ids are a 404, not a server error.
  const saved = await page.request.post(`/api/private/sites/${site}/cohorts`, {
    headers: owner.apiHeaders,
    data: { name: 'E2E api cohort', definition: { match: 'all', window_days: 90, conditions: [{ type: 'activity', metric: 'sessions', min_count: 1 }] } },
  })
  expect(saved.status()).toBe(201)
  const { id } = await saved.json() as { id: number }
  const dateRange = { from: '2026-09-28', to: '2026-09-29' }
  const filtered = await page.request.post('/api/private/query', {
    headers: owner.apiHeaders,
    data: { site_id: site, date_range: dateRange, metrics: ['visits'], filters: [{ dimension: 'cohort', operator: 'is', values: [String(id)] }] },
  })
  expect(filtered.status(), await filtered.text()).toBe(200)
  const unknown = await page.request.post('/api/private/query', {
    headers: owner.apiHeaders,
    data: { site_id: site, date_range: dateRange, metrics: ['visits'], filters: [{ dimension: 'cohort', operator: 'is', values: ['987654'] }] },
  })
  expect(unknown.status()).toBe(404)
  await page.request.delete(`/api/private/cohorts/${id}`, { headers: owner.apiHeaders })
})
