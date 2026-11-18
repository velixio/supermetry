import { mkdirSync } from 'node:fs'

import { expect, test } from './fixtures'
import { setTheme } from './theme'

const screenshotDir = 'e2e/screenshots/polish'
const range = '?period=custom&from=2026-09-28&to=2026-09-30'

async function noHorizontalOverflow(page: import('@playwright/test').Page) {
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth), { message: 'page must not scroll horizontally' })
    .toBeLessThanOrEqual(0)
}

test('polished dashboard controls, hierarchy, redirect, and responsive layouts', async ({ page, owner }) => {
  mkdirSync(screenshotDir, { recursive: true })
  const dashboard = `/app/${owner.orgSlug}/${owner.siteSlug}${range}`
  await page.setViewportSize({ width: 1440, height: 1000 })
  await owner.login(page, dashboard)

  await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
  await expect(page.getByRole('button', { name: /date range/i })).toBeVisible()
  await expect(page.getByRole('button', { name: 'View options' })).toBeVisible()
  await expect(page.getByRole('button', { name: /Visitors/ })).toHaveAttribute('data-priority', 'primary')
  await expect(page.getByRole('button', { name: /Pageviews/ })).toHaveAttribute('data-priority', 'primary')
  await noHorizontalOverflow(page)
  await page.screenshot({ path: `${screenshotDir}/dashboard-1440-light.png`, fullPage: true })

  await setTheme(page, 'dark')
  await expect(page.locator('html')).toHaveClass(/dark/)
  await page.screenshot({ path: `${screenshotDir}/dashboard-1440-dark.png`, fullPage: true })
  await setTheme(page, 'light')

  await page.setViewportSize({ width: 1024, height: 900 })
  await noHorizontalOverflow(page)
  await page.screenshot({ path: `${screenshotDir}/dashboard-1024.png`, fullPage: true })

  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.getByRole('button', { name: 'Filters' })).toBeVisible()
  await page.getByRole('button', { name: 'Filters' }).click()
  const sheet = page.getByRole('dialog', { name: 'Filters' })
  // The dashboard keeps its date range beside the Filters button, so the sheet holds only the filters.
  await expect(sheet.getByRole('button', { name: /date range/i })).toHaveCount(0)
  // On the dashboard, Compare sits on the chart and View options at the end of the filter row.
  await expect(sheet.getByRole('combobox', { name: 'Compare' })).toHaveCount(0)
  await page.keyboard.press('Escape')
  await expect(page.getByRole('button', { name: /date range/i })).toBeVisible()
  await expect(page.getByTestId('chart-controls').getByRole('combobox', { name: 'Compare' })).toBeVisible()
  await expect(page.getByTestId('filters-trailing').getByRole('button', { name: 'View options' })).toBeVisible()
  await noHorizontalOverflow(page)
  await page.screenshot({ path: `${screenshotDir}/dashboard-390.png`, fullPage: true })

  await page.goto('/onboarding')
  await expect(page).toHaveURL(/\/app(?:\/|$)/)
})

test('Sessions exposes the same single mobile Filters sheet', async ({ page, owner }) => {
  const sessions = `/app/${owner.orgSlug}/${owner.siteSlug}/sessions${range}`
  await page.setViewportSize({ width: 390, height: 844 })
  await owner.login(page, sessions)
  await page.getByRole('button', { name: 'Filters' }).click()
  const sheet = page.getByRole('dialog', { name: 'Filters' })
  await expect(sheet.getByRole('button', { name: /date range/i })).toBeVisible()
  await expect(sheet.getByRole('combobox', { name: 'Compare' })).toBeVisible()
  await noHorizontalOverflow(page)
})
