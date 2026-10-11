import { mkdirSync } from 'node:fs'

import { expect, test } from './fixtures'

const screenshotDir = 'e2e/screenshots/where-when'
const range = '?period=custom&from=2026-09-28&to=2026-09-30&metric=visitors'

async function noHorizontalOverflow(page: import('@playwright/test').Page, width: number) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
}

async function settleBand(page: import('@playwright/test').Page) {
  const where = page.getByRole('region', { name: 'Where' })
  await expect(where.locator('path[role="button"][data-country]').first()).toBeVisible()
  await where.evaluate((element) => {
    window.scrollTo({ top: element.getBoundingClientRect().top + window.scrollY - 72 })
  })
  return where
}

async function screenshotLocator(locator: import('@playwright/test').Locator, path: string) {
  await locator.scrollIntoViewIfNeeded()
  await expect(locator).toBeVisible()
  await locator.screenshot({ path })
}

test('dashboard geography and weekly rhythm support pointer, keyboard, details, filtering, and responsive layouts', async ({ page, owner }) => {
  mkdirSync(screenshotDir, { recursive: true })
  const browserErrors: string[] = []
  page.on('console', (message) => {
    // The authentication bootstrap intentionally probes the session before login.
    if (message.type() === 'error' && !message.text().startsWith('Failed to load resource:')) {
      browserErrors.push(message.text())
    }
  })
  page.on('pageerror', (error) => browserErrors.push(error.message))
  page.on('response', (response) => {
    if (response.status() >= 500) browserErrors.push(`${response.status()} ${response.url()}`)
  })
  const url = `/app/${owner.orgSlug}/${owner.siteSlug}${range}`
  await page.setViewportSize({ width: 1440, height: 900 })
  await owner.login(page, url)

  const where = page.getByRole('region', { name: 'Where' })
  const rhythm = page.getByRole('region', { name: 'Weekly rhythm' })
  await expect(where).toBeVisible()
  await expect(rhythm).toBeVisible()
  await settleBand(page)

  const band = page.getByTestId('where-when-band')
  const whereItem = band.locator('[data-dashboard-band-item="where"]')
  const rhythmItem = band.locator('[data-dashboard-band-item="weekly-rhythm"]')
  const goalsItem = band.locator('[data-dashboard-band-item="goals-events"]')
  const whereBox = await whereItem.boundingBox()
  const rhythmBox = await rhythmItem.boundingBox()
  const goalsBox = await goalsItem.boundingBox()
  expect(whereBox).not.toBeNull()
  expect(rhythmBox).not.toBeNull()
  expect(goalsBox).not.toBeNull()
  expect(whereBox!.x).toBeLessThan(rhythmBox!.x)
  expect(whereBox!.width).toBeGreaterThan(rhythmBox!.width)
  expect(Math.abs(whereBox!.x - goalsBox!.x)).toBeLessThan(2)
  expect(goalsBox!.y).toBeGreaterThanOrEqual(whereBox!.y + whereBox!.height)
  const mapColumnBox = await where.locator('[data-map-column]').boundingBox()
  const whereCardBox = await where.boundingBox()
  expect(mapColumnBox).not.toBeNull()
  expect(whereCardBox).not.toBeNull()
  expect(mapColumnBox!.width / whereCardBox!.width).toBeGreaterThanOrEqual(0.55)
  expect(mapColumnBox!.height).toBeGreaterThanOrEqual(260)
  for (const name of await where.locator('[data-country-name]').all()) {
    await expect(name).toHaveCSS('white-space', 'normal')
    expect(await name.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true)
  }

  const country = where.locator('path[role="button"][data-country]').first()
  const countryCode = await country.getAttribute('data-country')
  expect(countryCode).toMatch(/^[A-Z]{2}$/)
  await country.hover()
  const mapTooltip = page.getByRole('tooltip')
  await expect(mapTooltip).toBeVisible()
  await expect(mapTooltip).toHaveCSS('pointer-events', 'none')
  expect(await mapTooltip.evaluate((element) => element.parentElement === document.body)).toBe(true)
  const mapTooltipBox = await mapTooltip.boundingBox()
  const countryBox = await country.boundingBox()
  expect(mapTooltipBox).not.toBeNull()
  expect(countryBox).not.toBeNull()
  expect(Math.abs((mapTooltipBox!.x + mapTooltipBox!.width / 2) - (countryBox!.x + countryBox!.width / 2))).toBeLessThan(140)
  await expect(where.getByRole('button', { name: /rank 1/i })).toHaveAttribute('data-active', 'true')
  await country.focus()
  await page.keyboard.press('Enter')
  await expect(page.getByRole('button', { name: 'Filter the dashboard to this country' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('button', { name: 'Filter the dashboard to this country' })).toHaveCount(0)

  const busyCell = rhythm.locator('[role="gridcell"][data-busiest="true"]')
  await expect(busyCell).toBeVisible()
  await busyCell.hover()
  const gridTooltip = page.getByRole('tooltip')
  await expect(gridTooltip).toBeVisible()
  await expect(gridTooltip).toHaveCSS('pointer-events', 'none')
  const tooltipBox = await gridTooltip.boundingBox()
  const cellBox = await busyCell.boundingBox()
  const axisBox = await rhythm.locator('[role="row"]').first().boundingBox()
  expect(tooltipBox).not.toBeNull()
  expect(cellBox).not.toBeNull()
  expect(axisBox).not.toBeNull()
  expect(tooltipBox!.y + tooltipBox!.height).toBeLessThanOrEqual(cellBox!.y)
  expect(tooltipBox!.y + tooltipBox!.height <= axisBox!.y || tooltipBox!.y >= axisBox!.y + axisBox!.height).toBe(true)
  await page.screenshot({ path: `${screenshotDir}/dashboard-1440-grid-tooltip.png` })
  await busyCell.focus()
  await page.keyboard.press('ArrowRight')
  await expect(rhythm.locator('[role="gridcell"]:focus')).toBeVisible()
  await expect(page.getByRole('tooltip')).toBeVisible()
  await rhythm.locator('[role="gridcell"]:focus').evaluate((element) => (element as HTMLElement).blur())
  await page.mouse.move(0, 0)
  await expect(page.getByRole('tooltip')).toHaveCount(0)

  await noHorizontalOverflow(page, 1440)
  await page.screenshot({ path: `${screenshotDir}/dashboard-1440-light.png` })
  await screenshotLocator(rhythm, `${screenshotDir}/weekly-rhythm-1440-light.png`)
  await page.getByRole('button', { name: 'Use dark theme' }).click()
  await expect(page.locator('html')).toHaveClass(/dark/)
  await settleBand(page)
  await page.screenshot({ path: `${screenshotDir}/dashboard-1440-dark.png` })

  await page.getByRole('button', { name: 'Use light theme' }).click()
  await country.click()
  await expect(page.getByRole('button', { name: 'Filter the dashboard to this country' })).toBeVisible()
  await settleBand(page)
  await page.screenshot({ path: `${screenshotDir}/dashboard-1440-selected.png` })
  await page.getByRole('button', { name: 'Filter the dashboard to this country' }).click()
  await expect.poll(() => new URL(page.url()).searchParams.getAll('f')).toContain(`country:is:${countryCode}`)

  await page.goto(url)
  await page.setViewportSize({ width: 1024, height: 900 })
  await settleBand(page)
  const mapColumn1024 = await where.locator('[data-map-column]').boundingBox()
  const whereCard1024 = await where.boundingBox()
  expect(mapColumn1024).not.toBeNull()
  expect(whereCard1024).not.toBeNull()
  expect(mapColumn1024!.width / whereCard1024!.width).toBeGreaterThanOrEqual(0.55)
  expect(mapColumn1024!.height).toBeGreaterThanOrEqual(260)
  for (const name of await where.locator('[data-country-name]').all()) {
    await expect(name).toHaveCSS('white-space', 'normal')
    expect(await name.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true)
  }
  await noHorizontalOverflow(page, 1024)
  await page.screenshot({ path: `${screenshotDir}/dashboard-1024-light.png` })

  await page.setViewportSize({ width: 390, height: 844 })
  await settleBand(page)
  const mobileWhere = await whereItem.boundingBox()
  const mobileRhythm = await rhythmItem.boundingBox()
  const mobileGoals = await goalsItem.boundingBox()
  expect(mobileWhere).not.toBeNull()
  expect(mobileRhythm).not.toBeNull()
  expect(mobileGoals).not.toBeNull()
  expect(mobileWhere!.y).toBeLessThan(mobileRhythm!.y)
  expect(mobileRhythm!.y).toBeLessThan(mobileGoals!.y)
  await noHorizontalOverflow(page, 390)
  await page.screenshot({ path: `${screenshotDir}/dashboard-390-light.png` })
  await rhythm.scrollIntoViewIfNeeded()
  await noHorizontalOverflow(page, 390)
  await page.screenshot({ path: `${screenshotDir}/dashboard-390-weekly-light.png` })
  await settleBand(page)
  await where.locator('path[role="button"][data-country]').first().click()
  const sheet = page.getByRole('dialog')
  await expect(sheet).toBeVisible()
  await expect(sheet).toHaveClass(/bg-popover/)
  await sheet.evaluate((element) => element.scrollTo({ top: element.scrollHeight }))
  await noHorizontalOverflow(page, 390)
  await page.screenshot({ path: `${screenshotDir}/dashboard-390-selected.png` })
  expect(browserErrors).toEqual([])
})
