import { spawn, type ChildProcess } from 'node:child_process'
import type { Browser, Page, Response, StorageState } from '@playwright/test'

import { expect, test, type Owner } from '../fixtures'

const runs = 7
const settleMs = 250

type PageMetrics = {
  requests: number
  jsRequests: number
  jsDecodedKB: number
  jsTransferKB: number
  lcpMs: number
  cls: number
  chartReadyMs: number | null
  consoleErrors: string[]
}

type VitalState = Window & {
  __supermetryPerf?: {
    cls: number
    lcp: number
    chartReady: number | null
  }
}

test.describe('dashboard performance', () => {
test.skip(process.env.SUPERMETRY_PERF !== '1', 'set SUPERMETRY_PERF=1 to run browser performance measurements')

function median(values: number[]): number {
  const sorted = [...values].sort((left, right) => left - right)
  return sorted[Math.floor(sorted.length / 2)] ?? 0
}

function summarize(samples: PageMetrics[]) {
  const numbers = <Key extends keyof PageMetrics>(key: Key) =>
    samples.map((sample) => sample[key]).filter((value): value is number => typeof value === 'number')
  const range = (values: number[]) => ({ min: Math.min(...values), median: median(values), max: Math.max(...values) })
  const chartReady = numbers('chartReadyMs')
  return {
    requests: range(numbers('requests')),
    jsRequests: range(numbers('jsRequests')),
    jsDecodedKB: range(numbers('jsDecodedKB')),
    jsTransferKB: range(numbers('jsTransferKB')),
    lcpMs: range(numbers('lcpMs')),
    cls: range(numbers('cls')),
    chartReadyMs: chartReady.length ? range(chartReady) : null,
    consoleErrors: [...new Set(samples.flatMap((sample) => sample.consoleErrors))],
  }
}

async function startBaseline(binary: string, baseURL: string, schema: string): Promise<ChildProcess> {
  const testDSN = new URL(process.env.SUPERMETRY_TEST_DSN!)
  testDSN.searchParams.set('options', `-csearch_path=${schema}`)
  const child = spawn(binary, ['serve'], {
    env: {
      ...process.env,
      SUPERMETRY_DATABASE_URL: testDSN.toString(),
      SUPERMETRY_BASE_URL: baseURL,
      SUPERMETRY_LISTEN: new URL(baseURL).host,
      SUPERMETRY_SECRET_KEY: Buffer.alloc(32, 'e').toString('base64'),
    },
    stdio: 'ignore',
  })
  for (let attempt = 0; attempt < 100; attempt++) {
    if (child.exitCode !== null) throw new Error(`baseline server exited with ${child.exitCode}`)
    try {
      if ((await fetch(`${baseURL}/readyz`)).ok) return child
    } catch { /* server is still starting */ }
    await new Promise((resolve) => setTimeout(resolve, 50))
  }
  child.kill('SIGTERM')
  throw new Error('baseline server did not become ready')
}

async function installVitals(page: Page) {
  await page.addInitScript(() => {
    const state = { cls: 0, lcp: 0, chartReady: null as number | null }
    ;(window as VitalState).__supermetryPerf = state

    new PerformanceObserver((list) => {
      for (const entry of list.getEntries()) {
        state.lcp = entry.startTime
      }
    }).observe({ type: 'largest-contentful-paint', buffered: true })

    let sessionValue = 0
    let sessionStart = 0
    let lastShift = 0
    new PerformanceObserver((list) => {
      for (const entry of list.getEntries() as Array<PerformanceEntry & { hadRecentInput: boolean; value: number }>) {
        if (entry.hadRecentInput) continue
        if (sessionValue === 0 || entry.startTime - lastShift >= 1_000 || entry.startTime - sessionStart >= 5_000) {
          sessionValue = entry.value
          sessionStart = entry.startTime
        } else {
          sessionValue += entry.value
        }
        lastShift = entry.startTime
        state.cls = Math.max(state.cls, sessionValue)
      }
    }).observe({ type: 'layout-shift', buffered: true })

    const recordChartPaint = () => {
      if (state.chartReady !== null) return
      const chart = document.querySelector('[role="img"][aria-label$=" over time"] svg.recharts-surface')
      if (!chart) return
      requestAnimationFrame(() => requestAnimationFrame(() => {
        state.chartReady ??= performance.now()
      }))
    }
    addEventListener('DOMContentLoaded', () => {
      recordChartPaint()
      new MutationObserver(recordChartPaint).observe(document, { childList: true, subtree: true })
    }, { once: true })
  })
}

async function authenticatedState(browser: Browser, baseURL: string, owner: Owner): Promise<StorageState> {
  const context = await browser.newContext({ baseURL, extraHTTPHeaders: { Origin: baseURL } })
  const response = await context.request.post(`${baseURL}/api/private/auth/login`, {
    data: { email: owner.email, password: owner.password, device_label: 'Playwright performance' },
  })
  expect(response.ok(), await response.text()).toBe(true)
  const state = await context.storageState()
  await context.close()
  return state
}

async function measurePage(
  browser: Browser,
  baseURL: string,
  path: string,
  options: { storageState?: StorageState; chart?: boolean } = {},
): Promise<PageMetrics> {
  const context = await browser.newContext({
    baseURL,
    extraHTTPHeaders: { Origin: baseURL },
    colorScheme: 'light',
    reducedMotion: 'reduce',
    storageState: options.storageState,
  })
  const page = await context.newPage()
  await installVitals(page)

  let requests = 0
  let jsRequests = 0
  const decodedBodies: Array<Promise<number>> = []
  const consoleErrors: string[] = []

  page.on('request', () => requests++)
  page.on('console', (message) => {
    if (message.type() === 'error') consoleErrors.push(message.text())
  })
  page.on('pageerror', (error) => consoleErrors.push(error.message))
  page.on('response', (response: Response) => {
    const contentType = response.headers()['content-type'] ?? ''
    if (!/javascript|ecmascript/.test(contentType)) return
    jsRequests++
    decodedBodies.push(response.body().then((body) => body.byteLength))
  })

  await page.goto(path, { waitUntil: 'load' })
  let chartReadyMs: number | null = null
  if (options.chart) {
    const chart = page.getByRole('img', { name: 'Visitors over time' }).locator('svg.recharts-surface')
    await expect(chart).toBeVisible({ timeout: 30_000 })
    await expect.poll(() => page.evaluate(() => (window as VitalState).__supermetryPerf?.chartReady)).not.toBeNull()
    chartReadyMs = await page.evaluate(() => (window as VitalState).__supermetryPerf?.chartReady ?? null)
  } else {
    await expect(page.getByLabel('Email')).toBeVisible()
  }
  await page.waitForTimeout(settleMs)

  const decodedBytes = (await Promise.all(decodedBodies)).reduce((total, bytes) => total + bytes, 0)
  const browserMetrics = await page.evaluate(() => {
    const resources = performance.getEntriesByType('resource') as PerformanceResourceTiming[]
    const js = resources.filter((entry) => entry.initiatorType === 'script' || /\.js(?:$|\?)/.test(entry.name))
    const vitals = (window as VitalState).__supermetryPerf ?? { cls: 0, lcp: 0 }
    return {
      transferBytes: js.reduce((total, entry) => total + entry.transferSize, 0),
      lcp: vitals.lcp,
      cls: vitals.cls,
    }
  })

  await context.close()
  return {
    requests,
    jsRequests,
    jsDecodedKB: Number((decodedBytes / 1024).toFixed(1)),
    jsTransferKB: Number((browserMetrics.transferBytes / 1024).toFixed(1)),
    lcpMs: Math.round(browserMetrics.lcp),
    cls: Number(browserMetrics.cls.toFixed(5)),
    chartReadyMs: chartReadyMs === null ? null : Math.round(chartReadyMs),
    consoleErrors,
  }
}

test('reports interleaved cold main and branch ranges', async ({ browser, owner }, testInfo) => {
  test.setTimeout(180_000)
  const branchURL = String(testInfo.project.use.baseURL)
  const baselineBinary = process.env.SUPERMETRY_PERF_BASELINE_BINARY
  test.skip(!baselineBinary, 'set SUPERMETRY_PERF_BASELINE_BINARY to compare main and branch')
  const baselinePort = Number(new URL(branchURL).port) + 1
  const baselineURL = `http://127.0.0.1:${baselinePort}`
  const baselineServer = await startBaseline(baselineBinary!, baselineURL, owner.schema)
  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}?period=custom&from=2026-09-28&to=2026-09-30`
  const samples = {
    main: { login: [] as PageMetrics[], dashboard: [] as PageMetrics[] },
    branch: { login: [] as PageMetrics[], dashboard: [] as PageMetrics[] },
  }
  try {
    const sharedState = await authenticatedState(browser, branchURL, owner)
    const states = { main: sharedState, branch: sharedState }
    for (let run = 0; run < runs; run++) {
      for (const [name, baseURL] of [['main', baselineURL], ['branch', branchURL]] as const) {
        samples[name].login.push(await measurePage(browser, baseURL, '/login'))
        samples[name].dashboard.push(await measurePage(browser, baseURL, dashboard, { storageState: states[name], chart: true }))
      }
    }
  } finally {
    baselineServer.kill('SIGTERM')
  }
  const report = Object.fromEntries(Object.entries(samples).map(([name, pages]) => [name, {
    login: summarize(pages.login),
    dashboard: summarize(pages.dashboard),
  }]))
  console.log(`SUPERMETRY_PERF_RESULT ${JSON.stringify(report)}`)
  expect(report.main.login.chartReadyMs).toBeNull()
  expect(report.branch.dashboard.chartReadyMs).not.toBeNull()
})
})
