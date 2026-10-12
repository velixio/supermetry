import { mkdirSync, readFileSync } from 'node:fs'

import { expect, test } from './fixtures'

const range = { from: '2026-09-28', to: '2026-09-30' }
const shots = 'e2e/screenshots/full-report'

test('full report dialog searches, sorts, traps focus, restores focus and exports CSV and JSON', async ({ page, owner }) => {
  test.setTimeout(90_000)
  mkdirSync(shots, { recursive: true })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await owner.login(page, `/app/${owner.orgSlug}/${owner.siteSlug}?period=custom&from=${range.from}&to=${range.to}`)
  const card = page.getByRole('region', { name: 'Location' })
  const trigger = card.getByRole('button', { name: 'View full report' })
  await trigger.focus()
  await page.keyboard.press('Enter')

  const dialog = page.getByRole('dialog', { name: /Location: full report/ })
  await expect(dialog).toBeVisible()
  const table = dialog.getByRole('table')
  await expect(table.getByRole('row').nth(1)).toBeVisible()
  const total = await table.getByRole('row').count()
  await page.screenshot({ path: `${shots}/1440-light.png` })

  // Search narrows the rows.
  await dialog.getByRole('searchbox').fill('india')
  await expect(table.getByRole('row')).toHaveCount(2)
  await expect(table).toContainText('India')
  await dialog.getByRole('searchbox').fill('')
  await expect(table.getByRole('row')).toHaveCount(total)

  // Sort by the country column ascending then descending.
  const names = async () => (await table.locator('tbody tr td:first-child').allInnerTexts()).map((text) => text.trim())
  const header = table.getByRole('button', { name: /^Country/ })
  await header.click()
  await expect(table.getByRole('columnheader', { name: /Country/ })).toHaveAttribute('aria-sort', 'ascending')
  const ascending = await names()
  await header.click()
  await expect(table.getByRole('columnheader', { name: /Country/ })).toHaveAttribute('aria-sort', 'descending')
  const descending = await names()
  expect(descending).toEqual([...ascending].reverse())

  // Focus stays inside the dialog while tabbing.
  for (let i = 0; i < 25; i += 1) {
    await page.keyboard.press('Tab')
    expect(await page.evaluate(() => !!document.activeElement?.closest('[role="dialog"]'))).toBe(true)
  }

  // CSV with only the dimension and one chosen column.
  await expect(dialog.getByLabel('Country', { exact: true })).toBeChecked()
  const csvPromise = page.waitForEvent('download')
  await dialog.getByRole('button', { name: 'Download CSV' }).click()
  const csv = await csvPromise
  expect(csv.suggestedFilename()).toContain(owner.siteSlug)
  expect(csv.suggestedFilename()).toContain(range.from)
  expect(csv.suggestedFilename()).toContain(range.to)
  const csvText = readFileSync((await csv.path())!, 'utf8')
  expect(csvText.split(/\r?\n/)[0]).toMatch(/country/i)
  expect(csvText).toMatch(/India|IN/)

  // JSON with the Visitors column unchecked: only the dimension remains.
  await dialog.getByLabel('Visitors', { exact: true }).uncheck()
  const jsonPromise = page.waitForEvent('download')
  await dialog.getByRole('button', { name: 'Download JSON' }).click()
  const json = await jsonPromise
  expect(json.suggestedFilename()).toBe(`${owner.siteSlug}-country-${range.from}-${range.to}.json`)
  const parsed = JSON.parse(readFileSync((await json.path())!, 'utf8')) as { rows: Array<Record<string, unknown>>; date_range: typeof range }
  expect(parsed.date_range).toEqual(range)
  expect(parsed.rows.length).toBeGreaterThan(0)
  expect(Object.keys(parsed.rows[0])).toEqual(['country'])
  await dialog.getByLabel('Country', { exact: true }).uncheck()
  await dialog.getByLabel('Visitors', { exact: true }).uncheck()
  await dialog.getByRole('button', { name: 'Download JSON' }).click()
  await expect(dialog.getByRole('alert')).toContainText('Choose at least one column')

  await page.evaluate(() => document.documentElement.classList.add('dark'))
  await page.screenshot({ path: `${shots}/1440-dark.png` })

  // Escape closes and focus returns to the trigger.
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
  await expect(trigger).toBeFocused()

  // Active filters are honoured by the export.
  await card.getByRole('button', { name: /^India/ }).click()
  await expect.poll(() => new URL(page.url()).searchParams.getAll('f')).toContain('country:is:IN')
  await page.getByRole('region', { name: 'Location' }).getByRole('button', { name: 'View full report' }).click()
  const filtered = page.getByRole('dialog', { name: /Location: full report/ })
  const filteredJson = page.waitForEvent('download')
  await filtered.getByRole('button', { name: 'Download JSON' }).click()
  const filteredBody = JSON.parse(readFileSync((await (await filteredJson).path())!, 'utf8')) as { filters: unknown[]; rows: Array<Record<string, unknown>> }
  expect(filteredBody.filters).toHaveLength(1)
  expect(filteredBody.rows.every((row) => row.country === 'IN')).toBe(true)

  await page.setViewportSize({ width: 1024, height: 800 })
  await page.screenshot({ path: `${shots}/1024.png` })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.screenshot({ path: `${shots}/390.png` })
  expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0)
})
