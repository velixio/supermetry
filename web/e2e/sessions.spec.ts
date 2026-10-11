import { mkdirSync } from 'node:fs'

import { expect, test } from './fixtures'

const screenshotDir = 'e2e/screenshots/sessions'

const session = {
  id: '301',
  started_at: '2026-10-08T08:00:00Z',
  duration_ms: 120000,
  pageviews: 2,
  events: 1,
  entry_page: '/',
  exit_page: '/pricing',
  source: 'Google',
  country: 'US',
  city: 'San Francisco',
  device: 'desktop',
  browser: 'Chrome',
  os: 'Linux',
  external_id: 'ada-42',
  has_replay: true,
}

test('Sessions stays contained and switches to cards below 768px', async ({ page, owner }) => {
  mkdirSync(screenshotDir, { recursive: true })
  await page.route(`**/api/private/sites/${owner.siteId}/sessions?**`, async (route) => {
    await route.fulfill({ json: { data: [session], next_cursor: null, total: 1, total_capped: false } })
  })
  await page.route('**/api/private/sessions/301?**', async (route) => {
    await route.fulfill({
      json: {
        session,
        timeline: [
          { ts: session.started_at, kind: 'pageview', name: 'pageview', page: '/', props: null },
          { ts: '2026-10-08T08:01:00Z', kind: 'pageview', name: 'pageview', page: '/pricing', props: null },
        ],
      },
    })
  })

  const url = `/app/${owner.orgSlug}/${owner.siteSlug}/sessions?period=custom&from=2026-10-01&to=2026-10-08&compare=off&metric=pageviews&unknown=ignored`
  await page.setViewportSize({ width: 1440, height: 900 })
  await owner.login(page, url)
  await expect(page.getByRole('table', { name: 'Sessions' })).toBeVisible()

  for (const viewport of [
    { width: 1440, height: 900, name: 'sessions-1440-light.png' },
    { width: 1024, height: 768, name: 'sessions-1024.png' },
    { width: 768, height: 768, name: 'sessions-768.png' },
    { width: 390, height: 844, name: 'sessions-390.png' },
  ]) {
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    if (viewport.width < 768) {
      await expect(page.getByRole('list', { name: 'Sessions' })).toBeVisible()
      await expect(page.getByRole('table', { name: 'Sessions' })).toBeHidden()
    } else {
      await expect(page.getByRole('table', { name: 'Sessions' })).toBeVisible()
    }
    await page.screenshot({ path: `${screenshotDir}/${viewport.name}` })
  }

  const card = page.getByRole('listitem', { name: /session started/i })
  await card.getByRole('button', { name: 'View session 301' }).click()
  await expect(page.getByRole('dialog', { name: 'Session timeline' })).toBeVisible()
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({ path: `${screenshotDir}/sessions-390-card-tapped.png` })
})
