export type SitesOverviewSort = 'name' | 'visitors' | 'change' | 'created' | 'last_event'
export type SitesOverviewView = 'cards' | 'table'

export type SitesOverviewSearch = {
  q?: string
  org?: string
  period?: '24h' | '7d' | '30d'
  sort?: SitesOverviewSort
}

const periods = new Set(['24h', '7d', '30d'])
const sorts = new Set<SitesOverviewSort>(['name', 'visitors', 'change', 'created', 'last_event'])

export function parseSitesOverviewSearch(value: unknown): SitesOverviewSearch {
  const search = value && typeof value === 'object' ? value as Record<string, unknown> : {}
  return {
    ...(typeof search.q === 'string' && search.q ? { q: search.q.slice(0, 120) } : {}),
    ...(typeof search.org === 'string' && search.org ? { org: search.org.slice(0, 120) } : {}),
    ...(typeof search.period === 'string' && periods.has(search.period) ? { period: search.period as SitesOverviewSearch['period'] } : {}),
    ...(typeof search.sort === 'string' && sorts.has(search.sort as SitesOverviewSort) ? { sort: search.sort as SitesOverviewSort } : {}),
  }
}
