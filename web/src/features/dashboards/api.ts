import { useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'
import type { components } from '@/lib/api.gen'

export type Dashboard = components['schemas']['Dashboard']
export type DashboardLayout = components['schemas']['DashboardLayout']
export type DashboardWidget = components['schemas']['DashboardWidget']
type Problem = components['schemas']['Problem']

export const dashboardKeys = {
  list: (siteId: number) => ['dashboards', siteId] as const,
}

/** Saved dashboards of a site, or the built-in dashboard alone when none exist. */
export function useDashboards(siteId: number) {
  return useQuery<Dashboard[], Problem>({
    queryKey: dashboardKeys.list(siteId),
    queryFn: async ({ signal }) => {
      const { data, error } = await api.GET('/api/private/sites/{site}/dashboards', {
        params: { path: { site: siteId } },
        signal,
      })
      if (error) throw error
      if (!data) throw new Error('The dashboards response was empty.')
      return data.data
    },
    staleTime: 60_000,
  })
}
