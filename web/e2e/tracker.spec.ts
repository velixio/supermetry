import type { Page, Route } from '@playwright/test'
import { expect, test } from '@playwright/test'

type EventPayload = { name?: string; url?: string; props?: Record<string, string> }

async function openTracker(
  page: Page,
  attributes = '',
): Promise<EventPayload[]> {
  const events: EventPayload[] = []
  const scriptVersion = `${Date.now()}-${Math.random()}`
  await page.addInitScript(() => {
    Object.defineProperty(Navigator.prototype, 'webdriver', {
      configurable: true,
      get: () => false,
    })
  })
  await page.route('**/api/public/v1/events', async (route: Route) => {
    const body = route.request().postData()
    if (body) events.push(JSON.parse(body) as EventPayload)
    await route.fulfill({ status: 202 })
  })
  await page.route('**/tracker-test*', async (route: Route) => {
    await route.fulfill({
      contentType: 'text/html',
      body: `<!doctype html><title>Tracker test</title><script src="/tracker.js?e2e=${scriptVersion}" data-site="abc123def456" data-allow-localhost ${attributes}></script><main>Tracker test content</main>`,
    })
  })
  await page.goto(`/tracker-test?case=${Date.now()}`)
  return events
}

test('full tracker emits FCP and TTFB once for the document load', async ({ page }) => {
  const events = await openTracker(page)
  await expect.poll(() => page.evaluate(() => performance.getEntriesByName('first-contentful-paint').length)).toBe(1)

  await page.evaluate(() => window.dispatchEvent(new PageTransitionEvent('pagehide')))
  await expect.poll(() => events.filter((event) => event.name === 'Web Vitals').length).toBe(1)
  const props = events.find((event) => event.name === 'Web Vitals')?.props
  expect(Number(props?.fcp)).toBeGreaterThan(0)
  expect(Number(props?.ttfb)).toBeGreaterThanOrEqual(0)

  await page.evaluate(() => window.dispatchEvent(new PageTransitionEvent('pagehide')))
  await page.waitForTimeout(50)
  expect(events.filter((event) => event.name === 'Web Vitals')).toHaveLength(1)
})

function pageviews(events: EventPayload[]): EventPayload[] {
  return events.filter((event) => event.name === 'pageview')
}

async function expectPageviews(events: EventPayload[], count: number) {
  await expect.poll(() => pageviews(events).length).toBe(count)
}

test('load, pushState, replaceState, back and forward each track one pageview', async ({ page }) => {
  const events = await openTracker(page)
  await expectPageviews(events, 1)

  await page.evaluate(() => history.pushState({}, '', '/pushed'))
  await expectPageviews(events, 2)
  await page.evaluate(() => history.replaceState({}, '', '/replaced'))
  await expectPageviews(events, 3)
  await page.evaluate(() => history.replaceState({}, '', location.href))
  await page.waitForTimeout(100)
  expect(pageviews(events)).toHaveLength(3)

  await page.goBack()
  await expectPageviews(events, 4)
  await page.goForward()
  await expectPageviews(events, 5)

  await page.evaluate((urls) => {
    document.body.innerHTML = `<main style="font: 16px system-ui; max-width: 720px; margin: auto; padding: 48px"><h1>Tracker navigation verified</h1><p>Load, pushState, replaceState, back, and forward each emitted exactly one pageview.</p><ol>${urls.map((url) => `<li>${url}</li>`).join('')}</ol></main>`
  }, pageviews(events).map((event) => event.url))
})

test('hash changes follow the site hash-routing setting', async ({ page }) => {
  let events = await openTracker(page, 'data-hash="false"')
  await expectPageviews(events, 1)
  await page.evaluate(() => { location.hash = 'off' })
  await page.waitForTimeout(100)
  expect(pageviews(events)).toHaveLength(1)

  await page.unrouteAll({ behavior: 'wait' })
  events = await openTracker(page, 'data-hash="true"')
  await expectPageviews(events, 1)
  await page.evaluate(() => { location.hash = 'on' })
  await expectPageviews(events, 2)
})

test('manual mode never tracks automatically', async ({ page }) => {
  const events = await openTracker(page, 'data-manual="true"')
  await page.waitForTimeout(100)
  expect(events).toHaveLength(0)

  await page.evaluate(() => history.pushState({}, '', '/manual'))
  await page.waitForTimeout(100)
  expect(events).toHaveLength(0)

  await page.evaluate(() => window.supermetry.pageview())
  await expectPageviews(events, 1)
})

for (const signal of ['navigator DNT', 'window DNT', 'GPC'] as const) {
  test(`${signal} suppresses all sending and cannot be overridden`, async ({ page }) => {
    await page.addInitScript((name) => {
      if (name === 'navigator DNT') {
        Object.defineProperty(Navigator.prototype, 'doNotTrack', { configurable: true, get: () => '1' })
      } else if (name === 'window DNT') {
        Object.defineProperty(window, 'doNotTrack', { configurable: true, get: () => '1' })
      } else {
        Object.defineProperty(Navigator.prototype, 'globalPrivacyControl', { configurable: true, get: () => true })
      }
    }, signal)
    const events = await openTracker(page)
    await page.evaluate(() => {
      window.supermetry.optIn()
      window.supermetry.consent(true)
      window.supermetry.track('custom')
    })
    await page.waitForTimeout(100)
    expect(events).toHaveLength(0)
  })
}

test('consent mode queues sending until consent is granted', async ({ page }) => {
  const events = await openTracker(page, 'data-consent="required"')
  await page.waitForTimeout(100)
  expect(events).toHaveLength(0)

  await page.evaluate(() => window.supermetry.consent(true))
  await expectPageviews(events, 1)
})

test('opt-out stores only the documented key and suppresses sending', async ({ page }) => {
  const events = await openTracker(page)
  await expectPageviews(events, 1)
  await page.evaluate(() => {
    window.supermetry.optOut()
    window.supermetry.track('ignored')
  })
  await page.waitForTimeout(100)
  expect(events).toHaveLength(1)
  expect(await page.evaluate(() => Object.keys(localStorage))).toEqual(['supermetry_ignore'])
  expect(await page.evaluate(() => localStorage.getItem('supermetry_ignore'))).toBe('1')

  await page.evaluate(() => {
    document.body.innerHTML = '<main style="font: 16px system-ui; max-width: 720px; margin: auto; padding: 48px"><h1>Tracker privacy verified</h1><p>Opt-out suppressed subsequent events and stored only <code>supermetry_ignore=1</code>.</p></main>'
    window.scrollTo(0, 0)
  })
})
