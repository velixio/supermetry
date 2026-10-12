import { mkdirSync } from 'node:fs'

import { request as playwrightRequest } from '@playwright/test'

import { expect, test } from './fixtures'

const screenshotDirectory = 'e2e/screenshots/sites-overview'

const seededSites = 8

test('sites overview filters, remembers its view, exposes keyboard actions, and stays responsive', async ({ page, owner }, testInfo) => {
  mkdirSync(screenshotDirectory, { recursive: true })
  const baseURL = String(testInfo.project.use.baseURL)
  const api = await playwrightRequest.newContext({ baseURL, extraHTTPHeaders: { Origin: baseURL, ...owner.apiHeaders } })
  const seeded: number[] = []
  try {
    for (let index = 0; index < seededSites; index++) {
      const created = await api.post('/api/private/sites', {
        data: { name: `Overview seed ${index}`, hosts: [`overview-seed-${index}.test`], timezone: 'UTC', retention_days: 30, revenue_currency: 'USD' },
      })
      expect(created.ok(), await created.text()).toBe(true)
      const body = (await created.json()) as { id?: number; ID?: number }
      seeded.push(Number(body.id ?? body.ID))
    }
    await runOverview(page, owner)
  } finally {
    for (const id of seeded) await api.delete(`/api/private/sites/${id}`)
    await api.dispose()
  }
})

async function runOverview(page: import('@playwright/test').Page, owner: { login(page: import('@playwright/test').Page, next?: string): Promise<void> }) {
  const serverErrors: string[] = []
  page.on('response', (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`)
  })

  await owner.login(page, '/app')
  await expect(page.getByRole('heading', { name: 'Sites', exact: true })).toBeVisible()
  await expect(page.getByTestId('site-overview-card').first()).toBeVisible()
  expect(await page.getByTestId('site-overview-card').count()).toBeGreaterThanOrEqual(seededSites + 1)

  const actions = page.getByRole('button', { name: /^Actions for / }).first()
  await actions.focus()
  await page.keyboard.press('ArrowDown')
  await expect(page.getByRole('menuitem', { name: 'Open dashboard' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(actions).toBeFocused()

  const request = page.waitForResponse((response) => response.url().includes('/api/private/sites/overview?period=7d&limit=200') && response.ok())
  await page.getByRole('combobox', { name: 'Period', exact: true }).click()
  await page.getByRole('option', { name: '7 days' }).click()
  await request
  await expect(page).toHaveURL(/period=7d/)

  await page.getByRole('radio', { name: 'Table view' }).click()
  await expect(page.getByRole('table', { name: 'Sites overview' })).toBeVisible()
  await page.reload()
  await expect(page.getByRole('radio', { name: 'Table view' })).toHaveAttribute('aria-checked', 'true')
  await page.getByRole('radio', { name: 'Card view' }).click()
  await expect(page.getByTestId('site-overview-card').first()).toBeVisible()

  await page.setViewportSize({ width: 1440, height: 900 })
  await page.screenshot({ path: `${screenshotDirectory}/1440-light.png`, fullPage: true })
  await page.getByRole('button', { name: 'Use dark theme' }).click()
  await expect(page.locator('html')).toHaveClass(/dark/)
  await page.screenshot({ path: `${screenshotDirectory}/1440-dark.png`, fullPage: true })
  await page.getByRole('button', { name: 'Use light theme' }).click()

  await page.setViewportSize({ width: 1024, height: 850 })
  await page.screenshot({ path: `${screenshotDirectory}/1024.png`, fullPage: true })

  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.getByRole('heading', { name: 'Sites', exact: true })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
  await page.screenshot({ path: `${screenshotDirectory}/390.png`, fullPage: true })
  expect(serverErrors).toEqual([])
}
