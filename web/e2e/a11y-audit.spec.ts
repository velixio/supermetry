import { existsSync, mkdirSync, statSync, writeFileSync } from 'node:fs'
import { dirname } from 'node:path'

import type { Locator, Page } from '@playwright/test'

import { expect, test } from './fixtures'

test.use({ bypassCSP: true })

// The axe audit is opt-in: point SUPERMETRY_AXE_PATH at an axe-core build (axe.min.js).
// axe-core is deliberately not a project dependency, so the audit is skipped when it is not set.
const axePath = process.env.SUPERMETRY_AXE_PATH ?? ''
const screenshotDir = 'e2e/screenshots/a11y2'

type Impact = 'minor' | 'moderate' | 'serious' | 'critical'
type ImpactCounts = Record<Impact, number>

type AxeViolation = {
  id: string
  impact: Impact | null
  nodes: Array<{ target: string[]; html: string; failureSummary?: string }>
}

type AxePageReport = {
  page: string
  url: string
  counts: ImpactCounts
  violations: Array<{
    id: string
    impact: Impact | null
    nodes: number
    samples: Array<{ target: string[]; html: string; failureSummary?: string }>
  }>
}

const emptyCounts = (): ImpactCounts => ({ critical: 0, serious: 0, moderate: 0, minor: 0 })

async function waitForDashboard(page: Page) {
  await expect(page.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeAttached()
  await expect(page.getByRole('region', { name: 'Where' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Weekly rhythm' })).toBeVisible()
  await expect(page.locator('[aria-label^="Loading "]')).toHaveCount(0)
  const where = page.getByRole('region', { name: 'Where' })
  await expect(where.locator('svg[aria-label="World map"]')).toBeVisible()
  await expect(where.locator('path[role="button"][data-country]').first()).toBeVisible()
}

async function waitForSessions(page: Page) {
  await expect(page.getByRole('heading', { level: 1, name: 'Sessions' })).toBeVisible()
  await expect(page.getByRole('table', { name: 'Sessions' })).toBeVisible()
  await expect(page.getByLabel('Loading sessions')).toHaveCount(0)
}

async function waitForSettings(page: Page) {
  await expect(page.getByRole('heading', { level: 1, name: 'Site settings' })).toBeVisible()
  await expect(page.getByLabel('Revenue currency')).toBeVisible()
}

async function audit(page: Page, name: string): Promise<AxePageReport> {
  await page.addScriptTag({ path: axePath })
  const violations = await page.evaluate(async () => {
    const axe = (window as typeof window & {
      axe: { run(): Promise<{ violations: AxeViolation[] }> }
    }).axe
    return (await axe.run()).violations
  })
  const counts = emptyCounts()
  for (const violation of violations) {
    if (violation.impact) counts[violation.impact] += violation.nodes.length
  }
  console.log(`AXE ${name} ${JSON.stringify(counts)}`)
  return {
    page: name,
    url: page.url(),
    counts,
    violations: violations.map(({ id, impact, nodes }) => ({
      id,
      impact,
      nodes: nodes.length,
      samples: nodes.slice(0, 5).map(({ target, html, failureSummary }) => ({ target, html, failureSummary })),
    })),
  }
}

function saveAxeReport(pages: AxePageReport[]) {
  const output = process.env.SUPERMETRY_AXE_OUTPUT
  if (!output) return
  const aggregate = emptyCounts()
  for (const page of pages) {
    for (const impact of Object.keys(aggregate) as Impact[]) aggregate[impact] += page.counts[impact]
  }
  mkdirSync(dirname(output), { recursive: true })
  writeFileSync(output, `${JSON.stringify({ pages, aggregate }, null, 2)}\n`)
}

async function screenshot(page: Page, filename: string, fullPage = true) {
  const path = `${screenshotDir}/${filename}`
  await page.screenshot({ path, fullPage, animations: 'disabled' })
  expect(statSync(path).size, `${path} should contain a rendered page`).toBeGreaterThan(5_000)
}

async function screenshotElement(locator: Locator, filename: string) {
  const path = `${screenshotDir}/${filename}`
  await expect(locator).toBeVisible()
  await locator.screenshot({ path, animations: 'disabled' })
  expect(statSync(path).size, `${path} should contain a rendered element`).toBeGreaterThan(5_000)
}

test('reports axe findings for the requested pages', async ({ page, owner }) => {
  test.skip(!axePath || !existsSync(axePath), 'set SUPERMETRY_AXE_PATH to an axe-core axe.min.js to run the audit')
  const reports: AxePageReport[] = []

  await page.goto('/login')
  await expect(page.getByRole('heading', { level: 1, name: 'Sign in to Supermetry' })).toBeVisible()
  reports.push(await audit(page, 'login'))

  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}`
  await owner.login(page, dashboard)
  await waitForDashboard(page)
  reports.push(await audit(page, 'dashboard'))

  await page.goto(`${dashboard}/sessions`)
  await waitForSessions(page)
  reports.push(await audit(page, 'sessions'))

  await page.goto(`${dashboard}/settings/general`)
  await waitForSettings(page)
  reports.push(await audit(page, 'settings'))

  saveAxeReport(reports)
})

test('the first dashboard Tab offers a skip link that focuses the sole main landmark', async ({ page, owner }) => {
  mkdirSync(screenshotDir, { recursive: true })
  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}`
  await page.setViewportSize({ width: 1440, height: 1_000 })
  await owner.login(page, dashboard)
  await page.goto(dashboard)
  await waitForDashboard(page)

  const skipLink = page.getByRole('link', { name: 'Skip to main content' })
  await page.keyboard.press('Tab')
  await expect(skipLink).toBeFocused()
  await screenshot(page, 'skip-link-focused.png', false)

  const main = page.getByRole('main', { name: 'Main content' })
  await expect(main).toHaveCount(1)
  await page.keyboard.press('Enter')
  await expect(main).toBeFocused()
})

test('captures the dashboard and grouped sidebar at the requested breakpoints', async ({ page, owner }) => {
  mkdirSync(screenshotDir, { recursive: true })
  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}`
  await page.setViewportSize({ width: 1440, height: 1000 })
  await owner.login(page, dashboard)
  await waitForDashboard(page)

  for (const name of ['Analyze', 'Behaviour', 'Conversion', 'Manage']) {
    await expect(page.getByRole('heading', { name, exact: true })).toBeVisible()
  }
  await screenshot(page, 'dashboard-1440-light.png')
  await screenshotElement(page.locator('[data-slot="sidebar-container"]'), 'sidebar-1440-grouped.png')
  const band = page.getByTestId('where-when-band')
  await band.scrollIntoViewIfNeeded()
  await screenshot(page, 'dashboard-band-1440-light.png', false)

  await page.getByRole('button', { name: 'Use dark theme' }).click()
  await expect(page.locator('html')).toHaveClass(/dark/)
  await page.evaluate(() => window.scrollTo({ top: 0 }))
  await screenshot(page, 'dashboard-1440-dark.png')
  await page.getByRole('button', { name: 'Use light theme' }).click()
  await expect(page.locator('html')).not.toHaveClass(/dark/)

  await page.setViewportSize({ width: 1024, height: 900 })
  await waitForDashboard(page)
  await page.evaluate(() => window.scrollTo({ top: 0 }))
  await screenshot(page, 'dashboard-1024-light.png')
  await band.evaluate((element) => window.scrollTo({ top: element.getBoundingClientRect().top + window.scrollY - 72 }))
  await screenshot(page, 'dashboard-band-1024-light.png', false)

  await page.setViewportSize({ width: 390, height: 844 })
  await waitForDashboard(page)
  await screenshot(page, 'dashboard-390-light.png')
  await page.setViewportSize({ width: 390, height: 1_100 })
  await page.getByRole('button', { name: 'Toggle sidebar' }).click()
  const mobileSidebar = page.getByRole('dialog', { name: 'Sidebar' })
  await expect(mobileSidebar.getByRole('heading', { name: 'Analyze' })).toBeVisible()
  await expect(mobileSidebar.getByRole('heading', { name: 'Manage' })).toBeVisible()
  await screenshotElement(mobileSidebar, 'sidebar-390-grouped.png')
})
