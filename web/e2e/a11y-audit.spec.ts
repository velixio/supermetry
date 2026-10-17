import { mkdirSync, statSync, writeFileSync } from 'node:fs'
import { dirname } from 'node:path'

import type { Locator, Page } from '@playwright/test'

import { axeAvailable, axeMissing, axePath } from './axe'
import { expect, test } from './fixtures'

test.use({ bypassCSP: true })

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
  test.skip(!axeAvailable, axeMissing)
  const reports: AxePageReport[] = []

  await page.goto('/login')
  await expect(page.getByRole('heading', { level: 1, name: 'Sign in to Supermetry' })).toBeVisible()
  reports.push(await audit(page, 'login'))

  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}`
  await owner.login(page, dashboard)
  await waitForDashboard(page)
  reports.push(await audit(page, 'dashboard'))
  await page.getByRole('button', { name: 'Open command palette' }).click()
  await expect(page.getByRole('dialog', { name: 'Command palette' })).toBeVisible()
  reports.push(await audit(page, 'command-palette'))
  await page.keyboard.press('Escape')

  await page.goto(`${dashboard}/sessions`)
  await waitForSessions(page)
  reports.push(await audit(page, 'sessions'))

  await page.goto(`${dashboard}/settings/general`)
  await waitForSettings(page)
  reports.push(await audit(page, 'settings'))

  await page.goto('/app/account/profile')
  await expect(page.getByRole('heading', { level: 1, name: 'Profile' })).toBeVisible()
  await expect(page.getByRole('main')).toHaveCount(1)
  reports.push(await audit(page, 'account'))

  saveAxeReport(reports)
})

async function expectNoSeriousViolations(page: Page, name: string) {
  const report = await audit(page, name)
  const serious = report.violations
    .filter((violation) => violation.impact === 'serious' || violation.impact === 'critical')
    .map((violation) => `${violation.id}: ${violation.samples.map((sample) => sample.target.join(' ')).join(' | ')}`)
  expect.soft(serious, `${name}: serious or critical axe violations`).toEqual([])
  return report
}

test('account recovery, invitation, first-run, account security and admin pages have no serious axe violations', async ({ page, owner, browser, request }) => {
  test.skip(!axeAvailable, axeMissing)
  test.setTimeout(120_000)
  const reports: AxePageReport[] = []

  // Signed out: the auth screens.
  await page.goto('/forgot')
  await expect(page.getByRole('heading', { level: 1, name: 'Reset your password' })).toBeVisible()
  reports.push(await expectNoSeriousViolations(page, 'forgot'))
  await page.getByLabel('Email').fill('nobody@supermetry.test')
  await page.getByRole('button', { name: 'Send reset link' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'If an account exists' })).toBeVisible()
  reports.push(await expectNoSeriousViolations(page, 'forgot-sent'))

  await page.goto('/reset/not-a-real-token')
  await expect(page.getByRole('heading', { level: 1, name: 'Choose a new password' })).toBeVisible()
  reports.push(await expectNoSeriousViolations(page, 'reset'))

  const invitation = await request.post(`/api/private/orgs/${owner.orgId}/invitations`, {
    headers: owner.apiHeaders,
    data: { email: `axe-invite-${Date.now()}@supermetry.test`, role: 'viewer', site_ids: [] },
  })
  expect(invitation.ok(), await invitation.text()).toBe(true)
  const token = new URL((await invitation.json()).link).searchParams.get('token')
  const guest = await browser.newContext({ bypassCSP: true })
  try {
    const invitePage = await guest.newPage()
    await invitePage.goto(`/invite/${token}`)
    await expect(invitePage.getByRole('heading', { level: 1, name: /^Join / })).toBeVisible()
    await expect(invitePage.getByLabel('Name', { exact: true })).toBeVisible()
    reports.push(await expectNoSeriousViolations(invitePage, 'invite'))
  } finally {
    await guest.close()
  }

  // Signed in.
  await owner.login(page, `/app/${owner.orgSlug}/sites/new`)
  await expect(page.getByRole('heading', { level: 1, name: 'What do you want to measure?' })).toBeVisible()
  await page.getByText('Advanced options').click()
  await expect(page.getByLabel('Raw data retention')).toBeVisible()
  reports.push(await expectNoSeriousViolations(page, 'first-site'))

  await page.goto(`/app/${owner.orgSlug}/${owner.siteSlug}/settings/installation`)
  await expect(page.getByRole('heading', { level: 1, name: 'Installation' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Copy tracking snippet' })).toBeVisible()
  reports.push(await expectNoSeriousViolations(page, 'installation'))

  await page.goto(`/app/${owner.orgSlug}/settings/members`)
  await expect(page.getByRole('heading', { level: 1, name: 'Members' })).toBeVisible()
  await expect(page.getByText('(you)')).toBeVisible()
  reports.push(await expectNoSeriousViolations(page, 'members'))

  await page.goto(`/app/${owner.orgSlug}/settings/invites`)
  await expect(page.getByRole('heading', { level: 1, name: 'Invitations' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Sent invitations' })).toBeVisible()
  reports.push(await expectNoSeriousViolations(page, 'invitations'))

  await page.goto('/app/account/profile')
  await expect(page.getByRole('heading', { level: 1, name: 'Profile' })).toBeVisible()
  reports.push(await expectNoSeriousViolations(page, 'account-profile'))

  await page.goto('/app/account/security')
  await expect(page.getByRole('heading', { level: 1, name: 'Account security' })).toBeVisible()
  reports.push(await expectNoSeriousViolations(page, 'account-security'))

  for (const [path, heading] of [['/app/admin/users', 'Users'], ['/app/admin/organizations', 'Organizations'], ['/app/admin/sites', 'Sites']] as const) {
    await page.goto(path)
    await expect(page.getByRole('heading', { level: 1, name: heading })).toBeVisible()
    await expect(page.getByRole('table').first()).toBeVisible()
    reports.push(await expectNoSeriousViolations(page, `admin${path.slice('/app/admin'.length).replace('/', '-')}`))
  }

  await page.goto(`/app/${owner.orgSlug}/no-such-site`)
  await expect(page.getByRole('heading', { level: 1, name: 'Site not found' })).toBeVisible()
  reports.push(await expectNoSeriousViolations(page, 'site-not-found'))

  await page.goto('/no-such-page')
  await expect(page.getByRole('heading', { level: 1, name: 'Page not found' })).toBeVisible()
  await expect(page.getByRole('main')).toHaveCount(1)
  reports.push(await expectNoSeriousViolations(page, 'not-found'))

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

  for (const name of ['Analyze', 'Behaviour', 'Conversion', 'Manage', 'Settings']) {
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
