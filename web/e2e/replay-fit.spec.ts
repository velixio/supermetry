import { mkdirSync } from 'node:fs'

import type { Page, Route } from '@playwright/test'

import { expect, test, type Owner } from './fixtures'
import {
  desktopRecording,
  desktopReplayId,
  mobileRecording,
  mobileReplayId,
  resizingRecording,
  resizingReplayId,
} from './replay-recordings'

const screenshotDir = 'e2e/screenshots/replay-fit'

function session(id: string, device: 'desktop' | 'mobile') {
  return {
    id,
    started_at: '2026-10-11T14:45:00Z',
    duration_ms: 45_000,
    pageviews: 2,
    events: 1,
    entry_page: '/',
    exit_page: '/pricing',
    source: 'Direct',
    country: 'IN',
    city: 'Bengaluru',
    device,
    browser: 'Chrome',
    os: device === 'mobile' ? 'Android' : 'Linux',
    has_replay: true,
  }
}

async function mockReplays(page: Page, owner: Owner) {
  await page.route(`**/api/private/sites/${owner.siteId}/replays?**`, async (route) => {
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        data: [
          { session_id: desktopReplayId, started_at: '2026-10-11T14:45:00Z', duration_s: 45, pages: 2, bytes: desktopRecording.length, trigger: 'sampled', browser: 'Chrome', os: 'Linux', device: 'desktop', country: 'IN', city: 'Bengaluru', actions: 3 },
          { session_id: mobileReplayId, started_at: '2026-10-11T14:40:00Z', duration_s: 30, pages: 1, bytes: mobileRecording.length, trigger: 'sampled', browser: 'Chrome', os: 'Android', device: 'mobile', country: 'IN', city: 'Bengaluru', actions: 2 },
        ],
        next_cursor: null,
        total: 2,
        total_capped: false,
      }),
    })
  })
  await page.route(`**/api/private/sites/${owner.siteId}/replays/*/data`, async (route) => {
    const url = route.request().url()
    const recording = url.includes(`/${mobileReplayId}/`)
      ? mobileRecording
      : url.includes(`/${resizingReplayId}/`)
        ? resizingRecording
        : desktopRecording
    await route.fulfill({ contentType: 'application/x-ndjson', body: recording })
  })
  await page.route('**/api/private/sessions/*?**', async (route: Route) => {
    const id = new URL(route.request().url()).pathname.split('/').at(-1) ?? desktopReplayId
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        session: session(id, id === mobileReplayId ? 'mobile' : 'desktop'),
        timeline: [
          { ts: '2026-10-11T14:45:00Z', kind: 'pageview', name: 'pageview', page: '/' },
          { ts: '2026-10-11T14:45:20Z', kind: 'pageview', name: 'pageview', page: '/pricing' },
        ],
      }),
    })
  })
}

async function expectContained(page: Page) {
  const stage = page.getByTestId('replay-stage')
  const frame = stage.locator('.rr-player__frame')
  const iframe = stage.locator('iframe')
  await expect(iframe).toBeVisible()
  const [stageBox, frameBox, iframeBox] = await Promise.all([stage.boundingBox(), frame.boundingBox(), iframe.boundingBox()])
  expect(stageBox).not.toBeNull()
  expect(frameBox).not.toBeNull()
  expect(iframeBox).not.toBeNull()
  expect(Math.max(iframeBox!.width / frameBox!.width, iframeBox!.height / frameBox!.height)).toBeGreaterThanOrEqual(0.9)
  expect(await stage.evaluate((element) => getComputedStyle(element).backgroundColor)).not.toBe('rgb(255, 255, 255)')
}

async function expectDesktopRecording(page: Page) {
  await expect(page.getByTestId('replay-stage').locator('iframe').contentFrame().getByText('Desktop analytics, clearly framed')).toBeVisible()
}

async function waitForPaint(page: Page) {
  await page.evaluate(() => new Promise<void>((resolve) => {
    requestAnimationFrame(() => requestAnimationFrame(() => resolve()))
  }))
}

test.beforeAll(() => mkdirSync(screenshotDir, { recursive: true }))

test('replay modal fills its column and keeps details independently scrollable', async ({ page, owner }) => {
  await mockReplays(page, owner)
  await page.setViewportSize({ width: 1440, height: 900 })
  await owner.login(page, `/app/${owner.orgSlug}/${owner.siteSlug}/replays?period=30d&replay=${desktopReplayId}`)

  const playerRegion = page.getByRole('region', { name: 'Replay player' })
  const stage = page.getByTestId('replay-stage')
  const footer = page.locator('[data-slot="dialog-footer"]')
  await expectContained(page)
  await expectDesktopRecording(page)
  expect(await playerRegion.evaluate((element) => getComputedStyle(element).paddingTop)).toBe('0px')
  const [stageBox, footerBox] = await Promise.all([stage.boundingBox(), footer.boundingBox()])
  expect(footerBox!.y - (stageBox!.y + stageBox!.height)).toBeLessThanOrEqual(4)
  expect(await page.getByRole('complementary', { name: 'Replay details' }).evaluate((element) => getComputedStyle(element).overflowY)).toBe('auto')

  await waitForPaint(page)
  await page.screenshot({ path: `${screenshotDir}/modal-1440-light.png` })
  await page.evaluate(() => {
    localStorage.setItem('theme', 'dark')
    document.documentElement.classList.add('dark')
    document.documentElement.style.colorScheme = 'dark'
  })
  await expectContained(page)
  await expectDesktopRecording(page)
  await waitForPaint(page)
  await page.screenshot({ path: `${screenshotDir}/modal-1440-dark.png` })
})

test('wide and portrait recordings stay contained on the full page and mobile stack', async ({ page, owner }) => {
  await mockReplays(page, owner)
  await page.setViewportSize({ width: 1024, height: 768 })
  await owner.login(page, `/app/${owner.orgSlug}/${owner.siteSlug}/replays/${desktopReplayId}`)
  await expectContained(page)
  await page.screenshot({ path: `${screenshotDir}/page-1024.png`, fullPage: true })

  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto(`/app/${owner.orgSlug}/${owner.siteSlug}/replays?period=30d&replay=${mobileReplayId}`)
  await expectContained(page)
  const [playerBox, detailsBox] = await Promise.all([
    page.getByRole('region', { name: 'Replay player' }).boundingBox(),
    page.getByRole('complementary', { name: 'Replay details' }).boundingBox(),
  ])
  expect(detailsBox!.y).toBeGreaterThanOrEqual(playerBox!.y + playerBox!.height - 1)
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  await page.screenshot({ path: `${screenshotDir}/modal-390.png`, fullPage: true })
})

test('matching desktop aspect ratio does not leave wide side bars', async ({ page, owner }) => {
  await mockReplays(page, owner)
  await page.setViewportSize({ width: 1440, height: 900 })
  await owner.login(page, `/app/${owner.orgSlug}/${owner.siteSlug}/replays/${desktopReplayId}`)
  const stage = page.getByTestId('replay-stage')
  await stage.evaluate((element) => {
    element.style.width = '800px'
    element.style.height = '580px'
  })
  await expect.poll(async () => Number(await stage.getAttribute('data-replay-scale'))).toBeCloseTo(5 / 9, 2)
  const [frameBox, iframeBox] = await Promise.all([
    stage.locator('.rr-player__frame').boundingBox(),
    stage.locator('iframe').boundingBox(),
  ])
  expect(Math.abs(frameBox!.width - iframeBox!.width)).toBeLessThanOrEqual(4)
})

test('recorded viewport changes recompute the fit during playback', async ({ page, owner }) => {
  await mockReplays(page, owner)
  await owner.login(page, `/app/${owner.orgSlug}/${owner.siteSlug}/replays/${resizingReplayId}`)
  const stage = page.getByTestId('replay-stage')
  await expect(stage).toHaveAttribute('data-replay-viewport-width', '1440')
  await expect(stage).toHaveAttribute('data-replay-viewport-width', '390')
  await expect(stage).toHaveAttribute('data-replay-viewport-height', '844')
  await expectContained(page)
})
