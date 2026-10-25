import type { Page } from '@playwright/test'

import { expect, test } from './fixtures'

type MeSite = { id: number; name: string; slug?: string; public_id?: string; kind?: string }

async function openSwitcher(page: Page, siteName: string) {
  const sidebar = page.getByRole('complementary', { name: 'Application sidebar' })
  await sidebar.getByRole('button', { name: new RegExp(`^${siteName}`) }).click()
  return page.getByRole('menu')
}

test('switching site keeps the page, its portable search params, and falls back from a funnel to the list', async ({ page, owner, request }) => {
  const created = await request.post('/api/private/sites', {
    headers: owner.apiHeaders,
    data: { name: 'Switch target', hosts: ['switch-target.test'], timezone: 'UTC', retention_days: 30 },
  })
  expect(created.ok(), await created.text()).toBe(true)
  const other = (await created.json()) as MeSite
  const otherSlug = other.slug ?? other.public_id ?? String(other.id)
  const me = await request.get('/api/private/me', { headers: owner.apiHeaders })
  const current = ((await me.json()) as { memberships: Array<{ org: { slug: string }; sites: MeSite[] }> }).memberships
    .find((membership) => membership.org.slug === owner.orgSlug)!.sites.find((site) => site.id === owner.siteId)!
  const base = `/app/${owner.orgSlug}`

  const funnel = await request.post(`/api/private/sites/${owner.siteId}/funnels`, {
    headers: owner.apiHeaders,
    data: { name: 'Switch check', steps: [{ type: 'page', match: 'exact', value: '/' }, { type: 'page', match: 'prefix', value: '/pricing' }] },
  })
  expect(funnel.ok(), await funnel.text()).toBe(true)
  const funnelId = ((await funnel.json()) as { id: number }).id

  try {
    // Settings page.
    await owner.login(page, `${base}/${owner.siteSlug}/settings/tracking`)
    await (await openSwitcher(page, current.name)).getByRole('menuitem', { name: new RegExp(`^${other.name}`) }).click()
    await expect(page).toHaveURL((url) => url.pathname === `${base}/${otherSlug}/settings/tracking`)

    // Sessions with portable search params; site-specific ones are dropped.
    await page.goto(`${base}/${owner.siteSlug}/sessions?period=7d&compare=previous_period&dash=1`)
    await (await openSwitcher(page, current.name)).getByRole('menuitem', { name: new RegExp(`^${other.name}`) }).click()
    await expect(page).toHaveURL((url) => url.pathname === `${base}/${otherSlug}/sessions` &&
      url.searchParams.get('period') === '7d' && url.searchParams.get('compare') === 'previous_period' && !url.searchParams.has('dash'))

    // A funnel that only exists on the old site falls back to the funnels list.
    await page.goto(`${base}/${owner.siteSlug}/funnels/${funnelId}`)
    await (await openSwitcher(page, current.name)).getByRole('menuitem', { name: new RegExp(`^${other.name}`) }).click()
    await expect(page).toHaveURL((url) => url.pathname === `${base}/${otherSlug}/funnels`)
  } finally {
    await request.delete(`/api/private/sites/${owner.siteId}/funnels/${funnelId}`, { headers: owner.apiHeaders })
    await request.delete(`/api/private/sites/${other.id}`, { headers: owner.apiHeaders })
  }
})
