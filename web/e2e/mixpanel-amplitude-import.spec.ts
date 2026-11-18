import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { gzipSync } from 'node:zlib'

import type { Page } from '@playwright/test'

import { expect, test, type Owner } from './fixtures'

function sql(owner: Owner, statement: string) {
  if (!/^supermetry_e2e_\d+$/.test(owner.schema)) throw new Error('Expected an isolated synthetic schema')
  const dsn = new URL(process.env.SUPERMETRY_TEST_DSN!)
  dsn.searchParams.set('options', `-csearch_path=${owner.schema}`)
  return execFileSync('psql', [dsn.toString(), '-At', '-v', 'ON_ERROR_STOP=1', '-c', statement], { stdio: ['ignore', 'pipe', 'pipe'] }).toString().trim()
}

// Hand-computed from e2e/data/mixpanel-events.jsonl and amplitude-events.json
// (the same visits in two formats): 1 March has two visitors with three
// pageviews and one bounce, 2 March one bounced visitor with one pageview.
// Total visitors 3, visits 3, pageviews 4, bounces 2.
// The import wizard: choose the method and file, then step through the data range to the preview.
async function previewFile(page: Page, source: string, file: string | { name: string; mimeType: string; buffer: Buffer }) {
  await page.getByRole('tab', { name: 'Upload a file' }).click()
  await page.getByLabel('File type').selectOption(source)
  await page.getByLabel('Import file').setInputFiles(file)
  await page.getByRole('button', { name: 'Next' }).click()
  await page.getByRole('button', { name: 'Next' }).click()
}

test('mixpanel and amplitude imports: preview, confirm, history and undo', async ({ page, owner }) => {
  const base = `/app/${owner.orgSlug}/${owner.siteSlug}/settings/imports`
  const shots = 'e2e/screenshots/mixpanel-amplitude'
  await page.setViewportSize({ width: 1440, height: 1000 })
  await owner.login(page, base)
  await expect(page.getByRole('heading', { level: 1, name: /Imports/ })).toBeVisible()

  // Mixpanel JSON lines: 6 rows, line 6 is not JSON, line 7 has no distinct_id,
  // line 5 repeats an insert id.
  await page.getByRole('tab', { name: 'Upload a file' }).click()
  await page.getByLabel('File type').selectOption('mixpanel')
  await expect(page.getByText('What the file needs')).toBeVisible()
  await previewFile(page, 'mixpanel', resolve('e2e/data/mixpanel-events.jsonl'))
  const preview = page.getByRole('region', { name: 'Import historical data' })
  await expect(preview.getByRole('heading', { name: /Preview: nothing has been imported yet/ })).toBeVisible()
  await expect(preview).toBeVisible()
  await expect(preview.getByText('Row 6: invalid JSON')).toBeVisible()
  await expect(preview.getByText('Row 7: missing distinct_id')).toBeVisible()
  await expect(preview.getByText('2024-03-01 – 2024-03-02')).toBeVisible()
  expect(sql(owner, `select count(*) from imports where site_id=${owner.siteId}`)).toBe('0')
  await page.screenshot({ path: `${shots}/1440-light-mixpanel-preview.png`, fullPage: true })
  await preview.getByRole('button', { name: 'Import this range' }).click()
  const mixpanelRow = page.getByRole('row', { name: /mixpanel-events\.jsonl/ })
  await expect(mixpanelRow.getByText('Completed')).toBeVisible({ timeout: 30_000 })
  const totals = `select sum(visitors)||'/'||sum(visits)||'/'||sum(pageviews)||'/'||sum(bounces) from imported_day where site_id=${owner.siteId}`
  expect(sql(owner, totals)).toBe('3/3/4/2')

  // Amplitude export as a gzip (an hourly file), overlapping the same days:
  // the newer import wins per day, so the totals stay 3/3/4/2 and are not doubled.
  await expect(mixpanelRow.getByText('Completed')).toBeVisible()
  await page.getByRole('button', { name: 'Review the result' }).click()
  await page.getByRole('button', { name: 'Import more data' }).click()
  await previewFile(page, 'amplitude', {
    name: 'amplitude-events.json.gz',
    mimeType: 'application/gzip',
    buffer: gzipSync(readFileSync(resolve('e2e/data/amplitude-events.json'))),
  })
  const second = page.getByRole('region', { name: 'Import historical data' })
  await expect(second.getByRole('heading', { name: /Preview: nothing has been imported yet/ })).toBeVisible()
  await expect(second.getByText('Row 7: missing event_time')).toBeVisible()
  await expect(second.getByText('2 of these days are already covered by an earlier import.')).toBeVisible()
  await page.screenshot({ path: `${shots}/1440-light-amplitude-preview.png`, fullPage: true })
  await second.getByRole('button', { name: 'Replace the earlier import' }).click()
  const amplitudeRow = page.getByRole('row', { name: /amplitude-events\.json\.gz/ })
  await expect(amplitudeRow.getByText('Completed')).toBeVisible({ timeout: 30_000 })
  expect(sql(owner, `select count(*) from imports where site_id=${owner.siteId} and source='amplitude' and status='completed'`)).toBe('1')

  await page.emulateMedia({ colorScheme: 'dark' })
  await page.screenshot({ path: `${shots}/1440-dark-history.png`, fullPage: true })
  await page.setViewportSize({ width: 1024, height: 900 })
  await page.screenshot({ path: `${shots}/1024-dark.png`, fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({ path: `${shots}/390-dark.png`, fullPage: true })
  await page.emulateMedia({ colorScheme: 'light' })
  await page.setViewportSize({ width: 1440, height: 1000 })

  // Undo both; nothing imported is left.
  for (const name of ['amplitude-events.json.gz', 'mixpanel-events.jsonl']) {
    await page.getByRole('button', { name: `Undo import ${name}` }).click()
    await page.getByRole('button', { name: 'Undo import', exact: true }).click()
    await expect(page.getByRole('row', { name: new RegExp(name.replace('.', '\\.')) })).toHaveCount(0)
  }
  await expect(page.getByText('No imports yet.')).toBeVisible()
  expect(sql(owner, `select (select count(*) from imported_day)+(select count(*) from imported_day_dim)+(select count(*) from imports where site_id=${owner.siteId})`)).toBe('0')
  await page.screenshot({ path: `${shots}/1440-light-undone.png`, fullPage: true })
})
