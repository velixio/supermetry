import { mkdirSync } from 'node:fs'

import { expect, test } from './fixtures'
import { mobileRecording, mobileReplayId } from './replay-recordings'

const dir = 'e2e/screenshots/ui-fixes'
const range = '?period=custom&from=2026-09-28&to=2026-09-30&metric=visitors'

test.beforeAll(() => mkdirSync(dir, { recursive: true }))

test('map: opaque safe-area sheet on phones, cropped map keeps clickable countries', async ({ page, owner }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await owner.login(page, `/app/${owner.orgSlug}/${owner.siteSlug}${range}`)
  const where = page.getByRole('region', { name: 'Where' })
  const svg = where.locator('svg[role="group"]')
  await expect(svg).toHaveAttribute('viewBox', '0 12 1000 398')
  const country = where.locator('path[role="button"][data-country]').first()
  await country.scrollIntoViewIfNeeded()
  await country.focus()
  await page.keyboard.press('Enter')
  const filter = page.getByRole('button', { name: 'Filter the dashboard to this country' })
  await expect(filter).toBeVisible()
  const sheet = page.locator('[data-slot="sheet-content"]')
  const bg = await sheet.evaluate((el) => getComputedStyle(el).backgroundColor)
  expect(bg).not.toMatch(/rgba\(.*,\s*0(\.\d+)?\)$/)
  const actions = page.getByTestId('country-actions')
  expect(await actions.evaluate((el) => parseFloat(getComputedStyle(el).paddingBottom))).toBeGreaterThanOrEqual(16)
  expect(await actions.evaluate((el) => getComputedStyle(el).backgroundColor)).not.toMatch(/rgba\(.*,\s*0\)$/)
  const [fb, sb] = await Promise.all([filter.boundingBox(), sheet.boundingBox()])
  expect(fb!.y + fb!.height).toBeLessThanOrEqual(sb!.y + sb!.height - 12)
  expect(fb!.y).toBeGreaterThanOrEqual(sb!.y)
  await page.screenshot({ path: `${dir}/map-sheet-390.png` })
})

test('realtime: empty chart explains itself and Live now card has room at 1440', async ({ page, owner }) => {
  await page.route('**/api/private/realtime**', async (route) => {
    const response = await route.fetch()
    const body = await response.json()
    body.active_visitors = 0
    body.visitors_last_30m = 0
    body.pageviews_last_30m = 0
    const now = Date.now()
    body.visitors_per_minute = Array.from({ length: 30 }, (_, i) => ({ minute: new Date(now - (29 - i) * 60_000).toISOString(), visitors: 0 }))
    await route.fulfill({ response, json: body })
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  await owner.login(page, `/app/${owner.orgSlug}/${owner.siteSlug}/realtime`)
  await expect(page.getByTestId('realtime-empty')).toBeVisible()
  const cardBox = await page.getByTestId('realtime-empty').boundingBox()
  expect(cardBox!.width).toBeGreaterThan(300)
  const live = await page.locator('[class*="border-accent"]').first().boundingBox()
  expect(live!.width).toBeGreaterThanOrEqual(272)
  await page.screenshot({ path: `${dir}/realtime-empty-1440.png` })
})

test('replay: phone player uses the width and does not scroll sideways', async ({ page, owner }) => {
  await page.route(`**/api/private/sites/${owner.siteId}/replays/*/data`, (route) =>
    route.fulfill({ contentType: 'application/x-ndjson', body: mobileRecording }))
  await page.route('**/api/private/sessions/*?**', (route) => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({
      session: { id: mobileReplayId, started_at: '2026-10-11T14:40:00Z', duration_ms: 30_000, pageviews: 1, events: 1, entry_page: '/', exit_page: '/', source: 'Direct', country: 'IN', city: 'Bengaluru', device: 'mobile', browser: 'Chrome', os: 'Android', has_replay: true },
      timeline: [],
    }),
  }))
  await page.setViewportSize({ width: 390, height: 844 })
  await owner.login(page, `/app/${owner.orgSlug}/${owner.siteSlug}/replays?period=30d&replay=${mobileReplayId}`)
  const stage = page.getByTestId('replay-stage')
  await expect(stage.locator('iframe')).toBeVisible()
  const box = await stage.boundingBox()
  expect(box!.width).toBeGreaterThanOrEqual(385)
  const frame = await stage.locator('iframe').boundingBox()
  expect(frame!.width).toBeGreaterThanOrEqual(280)
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  await page.screenshot({ path: `${dir}/replay-390.png` })
})

test('chart: x-axis labels are 12px and do not overlap on phones', async ({ page, owner }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await owner.login(page, `/app/${owner.orgSlug}/${owner.siteSlug}${range}`)
  const ticks = page.locator('.recharts-xAxis-tick-labels .recharts-cartesian-axis-tick-value')
  await page.locator('[data-interval]').first().scrollIntoViewIfNeeded()
  await expect(ticks.first()).toBeVisible()
  expect(await ticks.first().evaluate((el) => el.getAttribute('font-size') ?? getComputedStyle(el).fontSize)).toMatch(/^12(px)?$/)
  const boxes = await ticks.evaluateAll((els) => els.map((el) => el.getBoundingClientRect()).sort((a, b) => a.left - b.left))
  for (let i = 1; i < boxes.length; i++) expect(boxes[i].left).toBeGreaterThanOrEqual(boxes[i - 1].right)
  await page.screenshot({ path: `${dir}/chart-390.png` })
})
