import type { APIRequestContext } from '@playwright/test'

import type { Owner } from './fixtures'

// The dashboard specs share one site, so each test starts and ends with no saved
// dashboards; otherwise a name used by one spec shows up twice in another.
export async function removeSavedDashboards(request: APIRequestContext, owner: Owner): Promise<void> {
  const base = `/api/private/sites/${owner.siteId}/dashboards`
  const response = await request.get(base, { headers: owner.apiHeaders })
  if (!response.ok()) return
  const { data } = (await response.json()) as { data: Array<{ id: number }> }
  for (const dashboard of data) {
    if (dashboard.id > 0) await request.delete(`${base}/${dashboard.id}`, { headers: owner.apiHeaders })
  }
}
