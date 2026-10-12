import { mkdirSync } from 'node:fs'

import { expect, test } from './fixtures'

const screenshotDir = 'e2e/screenshots/admin'

async function noHorizontalOverflow(page: import('@playwright/test').Page) {
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth), { message: 'page must not scroll horizontally' })
    .toBeLessThanOrEqual(0)
}

test('instance admin console lists users, organizations and sites and manages settings and the 2FA policy', async ({ page, owner }) => {
  mkdirSync(screenshotDir, { recursive: true })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await owner.login(page, '/app/admin')
  await expect(page).toHaveURL(/\/app\/admin\/users$/)
  await expect(page.getByRole('heading', { level: 1, name: 'Users' })).toBeVisible()
  await expect(page.getByTestId('admin-user-row').first()).toContainText(owner.email)
  await expect(page.getByText('Instance admin', { exact: true }).first()).toBeVisible()
  await expect(page.getByRole('button', { name: `Disable ${owner.email}` })).toBeDisabled()

  await page.getByLabel('Search', { exact: true }).fill('nobody-matches-this')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByText('Nothing matches this search.')).toBeVisible()
  await page.getByLabel('Search', { exact: true }).fill('')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByTestId('admin-user-row').first()).toBeVisible()
  await noHorizontalOverflow(page)
  await page.screenshot({ path: `${screenshotDir}/users-1440-light.png`, fullPage: true })

  await page.getByRole('link', { name: 'Organizations', exact: true }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Organizations' })).toBeVisible()
  await page.getByRole('button', { name: /^Details: / }).first().click()
  await expect(page.getByRole('button', { name: 'Close details' })).toBeVisible()

  await page.getByRole('link', { name: 'Sites', exact: true }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Sites' })).toBeVisible()
  await expect(page.getByRole('cell', { name: /Site 1/ }).first()).toBeVisible()

  await page.getByRole('link', { name: 'Instance settings', exact: true }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Instance settings' })).toBeVisible()
  const replay = page.getByRole('checkbox', { name: 'Show session replay in the interface' })
  const before = await replay.isChecked()
  await replay.click()
  await expect(replay).toBeChecked({ checked: !before })
  await expect(page.getByText('Saved.')).toBeVisible()
  await replay.click()
  await expect(replay).toBeChecked({ checked: before })

  await page.getByRole('link', { name: 'Security', exact: true }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Security' })).toBeVisible()
  try {
    await page.getByRole('radio', { name: 'Instance admins', exact: true }).check()
    await page.getByLabel('Grace period in days').fill('7')
    await page.getByRole('button', { name: 'Save policy' }).click()
    await expect(page.getByText('Saved.').first()).toBeVisible()
    await expect(page.getByRole('list').filter({ hasText: 'policy' }).first()).toBeVisible()
    await noHorizontalOverflow(page)
    await page.screenshot({ path: `${screenshotDir}/security-1440-light.png`, fullPage: true })
    await page.getByRole('button', { name: 'Use dark theme' }).click()
    await expect(page.locator('html')).toHaveClass(/dark/)
    await page.screenshot({ path: `${screenshotDir}/security-1440-dark.png`, fullPage: true })
    await page.getByRole('button', { name: 'Use light theme' }).click()
    await page.setViewportSize({ width: 1024, height: 900 })
    await noHorizontalOverflow(page)
    await page.screenshot({ path: `${screenshotDir}/security-1024.png`, fullPage: true })
    await page.setViewportSize({ width: 390, height: 844 })
    await noHorizontalOverflow(page)
    await page.screenshot({ path: `${screenshotDir}/security-390.png`, fullPage: true })
  } finally {
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.getByRole('radio', { name: 'Nobody', exact: true }).check()
    await page.getByRole('button', { name: 'Save policy' }).click()
    await expect(page.getByText('Saved.').first()).toBeVisible()
  }
})
