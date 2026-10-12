import { execFileSync } from 'node:child_process'
import { mkdirSync } from 'node:fs'

import { expect, test, type Owner } from './fixtures'

const screenshotDir = 'e2e/screenshots/replay-triage'

const replayIds = ['1683029196918025731', '1683029196918025732']

function seedReplayTriage(owner: Owner) {
  if (!/^supermetry_e2e_[0-9]+$/.test(owner.schema)) throw new Error('unexpected e2e schema')
  const sql = `
insert into sessions(site_id,retention_class,session_id,visitor_id,start,last,local_day,country,browser_id,device,pageviews,events)
values
  (${owner.siteId},30,${replayIds[0]},880001,'2026-10-11 14:45Z','2026-10-11 14:46:15Z','2026-10-11','IN',coalesce((select id from dict where kind=9 and value='Chrome' limit 1),0),1,3,2),
  (${owner.siteId},30,${replayIds[1]},880002,'2026-10-11 13:30Z','2026-10-11 13:30:30Z','2026-10-11','US',coalesce((select id from dict where kind=9 and value='Firefox' limit 1),0),0,1,0)
on conflict do nothing;
insert into replay_sessions(site_id,session_id,started_at,last_seen_at,day,bytes,pages,trigger,blob_key)
values
  (${owner.siteId},${replayIds[0]},'2026-10-11 14:45Z','2026-10-11 14:46:15Z','2026-10-11',1024,3,'rage-click','e2e-triage-one'),
  (${owner.siteId},${replayIds[1]},'2026-10-11 13:30Z','2026-10-11 13:30:30Z','2026-10-11',512,1,'sampled','e2e-triage-two')
on conflict do nothing;`
  execFileSync('psql', [String(process.env.SUPERMETRY_TEST_DSN), '-v', 'ON_ERROR_STOP=1', '-c', sql], {
    env: { ...process.env, PGOPTIONS: `-c search_path=${owner.schema}` },
    stdio: 'pipe',
  })
}

async function setDark(page: import('@playwright/test').Page) {
  await page.evaluate(() => {
    localStorage.setItem('theme', 'dark')
    document.documentElement.classList.add('dark')
    document.documentElement.style.colorScheme = 'dark'
  })
}

test.beforeAll(() => mkdirSync(screenshotDir, { recursive: true }))

test('filters, saves, layouts, URL state, and responsive cards remain usable', async ({ page, owner }) => {
  seedReplayTriage(owner)
  await page.setViewportSize({ width: 1440, height: 900 })
  await owner.login(page, `/app/${owner.orgSlug}/${owner.siteSlug}/replays?period=custom&from=2026-10-11&to=2026-10-11`)

  await expect(page.getByRole('heading', { level: 1, name: 'Replays' })).toBeVisible()
  await expect(page.getByRole('table', { name: 'Session replays' })).toBeVisible()
  await page.screenshot({ path: `${screenshotDir}/table-1440-light.png`, fullPage: true })

  const saveRequest = page.waitForRequest((request) => request.method() === 'PUT' && request.url().endsWith(`/${replayIds[0]}/saved`))
  await page.getByRole('table', { name: 'Session replays' }).getByRole('button', { name: 'Save replay' }).first().click()
  await saveRequest

  await page.getByRole('searchbox', { name: 'Search' }).fill('IN')
  await expect.poll(() => new URL(page.url()).searchParams.get('q')).toBe('IN')
  await expect(page.getByRole('table', { name: 'Session replays' }).getByRole('row')).toHaveCount(2)
  await page.getByRole('searchbox', { name: 'Search' }).fill('')
  await expect.poll(() => new URL(page.url()).searchParams.get('q')).toBeNull()

  await page.getByRole('radio', { name: 'Cards' }).click()
  await expect.poll(() => new URL(page.url()).searchParams.get('layout')).toBe('cards')
  await expect(page.getByRole('list', { name: 'Replay cards' })).toBeVisible()
  await page.screenshot({ path: `${screenshotDir}/cards-1440-light.png`, fullPage: true })
  await setDark(page)
  await page.screenshot({ path: `${screenshotDir}/cards-1440-dark.png`, fullPage: true })

  await page.setViewportSize({ width: 1024, height: 768 })
  await page.screenshot({ path: `${screenshotDir}/cards-1024-dark.png`, fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.getByRole('list', { name: 'Replay cards' })).toBeVisible()
  await expect(page.getByRole('radio', { name: 'Cards' })).toBeHidden()
  await page.screenshot({ path: `${screenshotDir}/cards-390-dark.png`, fullPage: true })

  await page.getByRole('radio', { name: 'Saved' }).click()
  await expect.poll(() => new URL(page.url()).searchParams.get('saved')).toBe('true')
  await expect(page.getByRole('list', { name: 'Replay cards' }).getByRole('listitem')).toHaveCount(1)
})
