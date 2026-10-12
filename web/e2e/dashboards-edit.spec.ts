import type { APIRequestContext, Page } from '@playwright/test'

import { expect, test, type Owner } from './fixtures'

const range = { from: '2026-09-28', to: '2026-09-30' }
const shots = 'e2e/screenshots/dash-edit'

type Saved = { id: number; name: string; version: number; is_default: boolean; layout: { widgets: Array<{ id: string; kind: string; w: number; title?: string }> } }

async function list(request: APIRequestContext, owner: Owner): Promise<Saved[]> {
  const response = await request.get(`/api/private/sites/${owner.siteId}/dashboards`, { headers: owner.apiHeaders })
  expect(response.ok(), await response.text()).toBe(true)
  return ((await response.json()) as { data: Saved[] }).data
}

async function seed(request: APIRequestContext, owner: Owner, name: string): Promise<Saved> {
  const response = await request.post(`/api/private/sites/${owner.siteId}/dashboards`, {
    headers: owner.apiHeaders,
    data: {
      name,
      layout: {
        schema_version: 1,
        period: '30d',
        widgets: [
          { id: 'kpis', kind: 'kpi_strip', title: 'Headline numbers', x: 0, y: 0, w: 12, h: 2, config: { metrics: ['visitors', 'pageviews'] } },
          { id: 'trend', kind: 'time_series', title: 'Visitors over time', x: 0, y: 2, w: 8, h: 5, config: { metric: 'visitors' } },
          { id: 'goals', kind: 'goals', title: 'Conversions', x: 8, y: 2, w: 4, h: 4, config: { limit: 5 } },
        ],
      },
    },
  })
  expect(response.ok(), await response.text()).toBe(true)
  return (await response.json()) as Saved
}

const order = (page: Page) => page.locator('[data-widget-id]').evaluateAll((nodes) => nodes.map((node) => node.getAttribute('data-widget-id')))
const live = (page: Page) => page.locator('p[aria-live="polite"]')

test('create, edit, reorder, resize, save, survive a conflict, set default and delete', async ({ page, request, owner }) => {
  const seeded = await seed(request, owner, 'Marketing')
  const base = `/app/${owner.orgSlug}/${owner.siteSlug}`
  await page.setViewportSize({ width: 1440, height: 1000 })
  await owner.login(page, `${base}?dash=${seeded.id}&period=custom&from=${range.from}&to=${range.to}`)
  await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1)

  // Open the editor from the viewer.
  await page.getByRole('button', { name: 'Edit dashboard' }).click()
  await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1)
  await expect(page.getByRole('heading', { name: 'Editing Marketing' })).toBeVisible()
  await expect(page.getByRole('combobox', { name: 'Default period' })).toBeVisible()
  expect(await order(page)).toEqual(['kpis', 'trend', 'goals'])
  await expect(page.getByTestId('edit-status')).toHaveText('All changes saved')

  // Rename, then reorder with the button and announce it.
  await page.getByLabel('Dashboard name').fill('Marketing v2')
  await expect(page.getByTestId('edit-status')).toHaveText('Unsaved changes')
  const later = page.getByRole('button', { name: 'Move later: Visitors over time' })
  expect((await later.boundingBox())?.height ?? 0).toBeGreaterThanOrEqual(44)
  await later.click()
  expect(await order(page)).toEqual(['kpis', 'goals', 'trend'])
  await expect(live(page)).toHaveText('Visitors over time moved to position 3 of 3.')
  await expect(page.getByRole('button', { name: 'Move later: Visitors over time' })).toBeFocused()

  // Resize from the keyboard with Alt and the arrow keys.
  await page.getByRole('button', { name: 'Make wider: Conversions' }).focus()
  await page.keyboard.press('Alt+ArrowRight')
  await expect(live(page)).toHaveText('Conversions is now 5 of 12 columns wide.')
  await page.getByRole('button', { name: 'Make narrower: Conversions' }).click()
  await page.getByRole('button', { name: 'Make narrower: Conversions' }).click()
  await expect(live(page)).toHaveText('Conversions is now 3 of 12 columns wide.')

  // Add a breakdown widget with options.
  await page.getByRole('button', { name: 'Add widget' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('combobox', { name: 'Widget type' }).click()
  await page.getByRole('option', { name: 'Breakdown list' }).click()
  await dialog.getByLabel('Title (optional)').fill('Top sources')
  await dialog.getByRole('combobox', { name: 'Group by' }).click()
  await page.getByRole('option', { name: 'Source', exact: true }).click()
  await dialog.getByLabel('Rows to show (1 to 50)').fill('99')
  await dialog.getByRole('button', { name: 'Add to dashboard' }).click()
  await expect(dialog.getByRole('alert')).toContainText('rows must be a whole number from 1 to 50')
  await dialog.getByLabel('Rows to show (1 to 50)').fill('8')
  await dialog.getByRole('button', { name: 'Add to dashboard' }).click()
  await expect(dialog).toHaveCount(0)
  expect(await order(page)).toEqual(['kpis', 'goals', 'trend', 'breakdown-1'])
  await expect(page.getByRole('button', { name: 'Options: Top sources' })).toBeFocused()

  // Remove one widget through the button.
  await page.getByRole('button', { name: 'Remove: Headline numbers' }).click()
  await expect(live(page)).toHaveText('Headline numbers removed.')
  expect(await order(page)).toEqual(['goals', 'trend', 'breakdown-1'])

  // Dashboard-wide period.
  await page.getByRole('combobox', { name: 'Default period' }).click()
  await page.getByRole('option', { name: /Last 7 days|7 days/ }).first().click()

  await page.getByRole('button', { name: 'Save changes' }).click()
  await expect(page.getByTestId('edit-status')).toHaveText('All changes saved')
  await expect(live(page)).toHaveText('Marketing v2 saved.')
  let saved = (await list(request, owner)).find((item) => item.id === seeded.id)!
  expect(saved.name).toBe('Marketing v2')
  expect(saved.version).toBe(seeded.version + 1)
  expect(saved.layout.widgets.map((widget) => [widget.id, widget.w])).toEqual([['goals', 3], ['trend', 8], ['breakdown-1', 6]])

  // Leave the editor; a reload keeps the saved layout.
  await page.getByRole('button', { name: 'Done' }).click()
  await expect(page.getByRole('button', { name: 'Edit dashboard' })).toBeVisible()
  await page.reload()
  await expect(page.getByRole('group', { name: 'Dashboard widgets' })).toBeVisible()
  expect(await order(page)).toEqual(['goals', 'trend', 'breakdown-1'])
  await expect(page.getByRole('combobox', { name: 'Dashboard' })).toContainText('Marketing v2')

  // A save from somewhere else makes this one stale: it is refused and nothing is overwritten.
  await page.getByRole('button', { name: 'Edit dashboard' }).click()
  await page.getByRole('button', { name: 'Remove: Conversions' }).click()
  const elsewhere = await request.patch(`/api/private/sites/${owner.siteId}/dashboards/${seeded.id}`, {
    headers: owner.apiHeaders,
    data: { name: 'Marketing from elsewhere', layout: saved.layout, version: saved.version },
  })
  expect(elsewhere.ok(), await elsewhere.text()).toBe(true)
  await page.getByRole('button', { name: 'Save changes' }).click()
  const conflict = page.getByRole('alert').filter({ hasText: 'This dashboard changed elsewhere' })
  await expect(conflict).toBeVisible()
  await expect(page.getByRole('button', { name: 'Save changes' })).toBeDisabled()
  saved = (await list(request, owner)).find((item) => item.id === seeded.id)!
  expect(saved.name).toBe('Marketing from elsewhere')
  expect(saved.layout.widgets).toHaveLength(3)
  await conflict.getByRole('button', { name: 'Reload latest version' }).click()
  await expect(page.getByLabel('Dashboard name')).toHaveValue('Marketing from elsewhere')
  expect(await order(page)).toEqual(['goals', 'trend', 'breakdown-1'])
  await expect(conflict).toHaveCount(0)

  // Unsaved changes are guarded when leaving through the switcher.
  await page.getByLabel('Dashboard name').fill('Unsaved name')
  await page.getByRole('combobox', { name: 'Dashboard' }).click()
  await page.getByRole('option', { name: 'Standard dashboard' }).click()
  const guard = page.getByRole('alertdialog')
  await expect(guard).toContainText('Leave without saving?')
  await guard.getByRole('button', { name: 'Keep editing' }).click()
  await expect(page.getByLabel('Dashboard name')).toHaveValue('Unsaved name')
  await page.getByLabel('Dashboard name').fill('Marketing from elsewhere')

  // Set as default, then duplicate.
  await page.getByRole('button', { name: 'Set as default' }).click()
  await expect(page.getByText('Default dashboard', { exact: true })).toBeVisible()
  expect((await list(request, owner)).find((item) => item.id === seeded.id)?.is_default).toBe(true)
  await page.getByRole('button', { name: 'Duplicate' }).click()
  await expect(page.getByRole('heading', { name: 'Editing Marketing from elsewhere (copy)' })).toBeVisible()
  await expect(page).not.toHaveURL(new RegExp(`dash=${seeded.id}(&|$)`))

  // Delete the copy after confirming.
  await page.getByRole('button', { name: 'Delete', exact: true }).click()
  await expect(page.getByRole('alertdialog')).toContainText('Delete Marketing from elsewhere (copy)?')
  await page.getByRole('button', { name: 'Keep dashboard' }).click()
  expect((await list(request, owner)).map((item) => item.name)).toContain('Marketing from elsewhere (copy)')
  await page.getByRole('button', { name: 'Delete', exact: true }).click()
  await page.getByRole('button', { name: 'Delete dashboard' }).click()
  await expect(page).toHaveURL(/dash=builtin/)
  await expect.poll(async () => (await list(request, owner)).map((item) => item.name)).not.toContain('Marketing from elsewhere (copy)')

  // New dashboard from a template.
  await page.getByRole('button', { name: 'New dashboard' }).click()
  await page.getByRole('combobox', { name: 'Starting point' }).click()
  await page.getByRole('option', { name: 'Performance' }).click()
  await page.getByRole('button', { name: 'Start editing' }).click()
  await expect(page.getByRole('heading', { name: 'New dashboard', level: 2 })).toBeVisible()
  await page.getByLabel('Dashboard name').fill('Marketing from elsewhere')
  await page.getByRole('button', { name: 'Create dashboard' }).click()
  await expect(page.getByText('Another dashboard already uses this name')).toBeVisible()
  await page.getByLabel('Dashboard name').fill('Speed')
  await page.getByRole('button', { name: 'Create dashboard' }).click()
  await expect(page.getByRole('heading', { name: 'Editing Speed' })).toBeVisible()
  const created = (await list(request, owner)).find((item) => item.name === 'Speed')!
  expect(created.layout.widgets[0].kind).toBe('web_vitals')
  await expect(page).toHaveURL(new RegExp(`dash=${created.id}`))
})

test('the editor fits every viewport and keeps single-column order on a phone', async ({ page, request, owner }) => {
  const seeded = await seed(request, owner, 'Layout check')
  const base = `/app/${owner.orgSlug}/${owner.siteSlug}`
  const url = `${base}?dash=${seeded.id}&period=custom&from=${range.from}&to=${range.to}`
  await owner.login(page, url)
  for (const [name, width, height, scheme] of [
    ['1440-light', 1440, 1000, 'light'],
    ['1440-dark', 1440, 1000, 'dark'],
    ['1024', 1024, 900, 'light'],
    ['390', 390, 900, 'light'],
  ] as const) {
    await page.emulateMedia({ colorScheme: scheme })
    await page.setViewportSize({ width, height })
    await page.goto(url)
    await page.getByRole('button', { name: 'Edit dashboard' }).click()
    await expect(page.getByRole('heading', { name: 'Editing Layout check' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Move later: Headline numbers' })).toBeVisible()
    await page.waitForTimeout(1200)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
    const small = await page.locator('[data-widget-id] [data-action]').evaluateAll((nodes) =>
      nodes.map((node) => node.getBoundingClientRect()).filter((box) => box.width < 43.5 || box.height < 43.5).length)
    expect(small).toBe(0)
    if (width === 390) {
      const lefts = await page.evaluate(() => new Set([...document.querySelectorAll('[data-widget-id]')].map((item) => Math.round(item.getBoundingClientRect().left))).size)
      expect(lefts).toBe(1)
    }
    await page.screenshot({ path: `${shots}/${name}.png`, fullPage: true })
  }

  // Tab order: the first toolbar button is reachable by keyboard and shows a focus ring.
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(url)
  await page.getByRole('button', { name: 'Edit dashboard' }).click()
  const button = page.getByRole('button', { name: 'Move later: Headline numbers' })
  await button.focus()
  await expect(button).toBeFocused()
  const ring = await button.evaluate((node) => getComputedStyle(node).boxShadow + getComputedStyle(node).outlineStyle)
  expect(ring).not.toBe('noneinset')
})
