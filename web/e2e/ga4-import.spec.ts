import { execFileSync } from 'node:child_process'
import { resolve } from 'node:path'

import { expect, test, type Owner } from './fixtures'

function sql(owner: Owner, statement: string) {
  if (!/^supermetry_e2e_\d+$/.test(owner.schema)) throw new Error('Expected an isolated synthetic schema')
  const dsn = new URL(process.env.SUPERMETRY_TEST_DSN!)
  dsn.searchParams.set('options', `-csearch_path=${owner.schema}`)
  return execFileSync('psql', [dsn.toString(), '-At', '-v', 'ON_ERROR_STOP=1', '-c', statement], { stdio: ['ignore', 'pipe', 'pipe'] }).toString().trim()
}

// Hand-computed from e2e/data/ga4-overview.csv: visitors 100+1200+80, sessions
// 150+200+100, views 400+500+250; line 5 ("oops") is the one bad row.
test('ga4 import: preview, confirm, history and undo', async ({ page, owner }) => {
  const base = `/app/${owner.orgSlug}/${owner.siteSlug}/settings/imports`
  await page.setViewportSize({ width: 1440, height: 1000 })
  await owner.login(page, base)
  await expect(page.getByRole('heading', { level: 1, name: /Imports/ })).toBeVisible()

  await page.getByLabel('Import source').selectOption('ga4')
  await expect(page.getByText('What the file needs')).toBeVisible()
  await page.getByLabel('Import file').setInputFiles(resolve('e2e/data/ga4-overview.csv'))
  await page.getByRole('button', { name: 'Preview import' }).click()

  const preview = page.getByRole('region', { name: /Preview: nothing has been imported yet/ })
  await expect(preview).toBeVisible()
  await expect(preview.getByText('Row 5: invalid date "oops"')).toBeVisible()
  await expect(preview.getByText('1,380')).toBeVisible()
  await expect(preview.getByText('450', { exact: true })).toBeVisible()
  await expect(preview.getByText('1,150')).toBeVisible()
  await expect(preview.getByText('2024-03-01 – 2024-03-03')).toBeVisible()
  expect(sql(owner, `select count(*) from imports where site_id=${owner.siteId}`)).toBe('0')
  await page.screenshot({ path: 'e2e/screenshots/ga4/1440-light-preview.png', fullPage: true })

  await preview.getByRole('button', { name: 'Confirm import' }).click()
  const row = page.getByRole('row', { name: /ga4-overview\.csv/ })
  await expect(row.getByText('Completed')).toBeVisible({ timeout: 30_000 })
  await expect(row.getByRole('cell', { name: '2024-03-01 – 2024-03-03' })).toBeVisible()
  expect(sql(owner, `select sum(visitors)||'/'||sum(visits)||'/'||sum(pageviews)||'/'||sum(bounces) from imported_day where site_id=${owner.siteId}`)).toBe('1380/450/1150/200')

  // Importing the same file again is refused by the server; the preview says so.
  await page.getByLabel('Import file').setInputFiles(resolve('e2e/data/ga4-overview.csv'))
  await page.getByRole('button', { name: 'Preview import' }).click()
  await expect(page.getByText(/This exact file was imported on/)).toBeVisible()
  await expect(page.getByText('3 of these days are already covered by an earlier import.')).toBeVisible()
  await page.getByRole('button', { name: 'Choose another file' }).click()

  await page.emulateMedia({ colorScheme: 'dark' })
  await page.screenshot({ path: 'e2e/screenshots/ga4/1440-dark-history.png', fullPage: true })
  await page.setViewportSize({ width: 1024, height: 900 })
  await page.screenshot({ path: 'e2e/screenshots/ga4/1024-dark.png', fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({ path: 'e2e/screenshots/ga4/390-dark.png', fullPage: true })
  await page.emulateMedia({ colorScheme: 'light' })
  await page.setViewportSize({ width: 1440, height: 1000 })

  await page.getByRole('button', { name: 'Undo import ga4-overview.csv' }).click()
  await page.getByRole('button', { name: 'Undo import', exact: true }).click()
  await expect(page.getByText('No imports yet.')).toBeVisible()
  expect(sql(owner, `select (select count(*) from imported_day)+(select count(*) from imports where site_id=${owner.siteId})`)).toBe('0')
  await page.screenshot({ path: 'e2e/screenshots/ga4/1440-light-undone.png', fullPage: true })
})
