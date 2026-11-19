import type { Locator, Page } from '@playwright/test'

const collapsedGroups = '[data-slot="sidebar"] [data-slot="sidebar-group-label"] button[aria-expanded="false"]'

/**
 * The sidebar collapses every group except the one holding the current page. Opens all of them,
 * so a link can be clicked or asserted absent no matter which group it sits in.
 */
export async function expandNavGroups(page: Page) {
  await page.locator('[data-slot="sidebar"] [data-slot="sidebar-group-label"] button').first().waitFor({ timeout: 5000 }).catch(() => undefined)
  for (let attempts = 0; attempts < 8; attempts++) {
    const closed = page.locator(collapsedGroups)
    if ((await closed.count()) === 0) return
    await closed.first().click()
  }
}

/** A sidebar link by exact name, after expanding every group (so `toHaveCount(0)` checks stay meaningful). */
export async function navLink(page: Page, name: string): Promise<Locator> {
  await expandNavGroups(page)
  return page.getByRole('link', { name, exact: true })
}
