import { api } from '@/lib/api'
import type { StatsQuery } from '@/lib/stats'

export function queryBody(query: StatsQuery) {
  return {
    site_id: query.siteId,
    date_range: { ...query.dateRange },
    ...(query.timezone === undefined ? {} : { timezone: query.timezone }),
    metrics: [...query.metrics],
    ...(query.dimensions ? { dimensions: [...query.dimensions] } : {}),
    filters: query.filters.map((filter) => ({
      dimension: filter.dimension,
      operator: filter.operator,
      values: [...filter.values],
    })),
    include_imported: query.includeImported !== false,
    ...(query.orderBy
      ? { order_by: query.orderBy.map((order) => ({ ...order })) }
      : {}),
    ...(query.limit === undefined ? {} : { limit: query.limit }),
    ...(query.cursor === undefined ? {} : { cursor: query.cursor }),
    ...(query.compare && query.compare !== 'off' ? { compare: query.compare } : {}),
  }
}

export async function downloadStatsCSV(query: StatsQuery): Promise<void> {
  const { data, error, response } = await api.POST('/api/private/query', {
    body: queryBody(query),
    headers: { Accept: 'text/csv' },
    params: { query: { format: 'csv', limit: 'all' } },
    parseAs: 'text',
  })
  if (error) throw error
  if (typeof data !== 'string' || typeof URL.createObjectURL !== 'function') return

  const url = URL.createObjectURL(new Blob([data], { type: 'text/csv;charset=utf-8' }))
  const link = document.createElement('a')
  link.href = url
  const filename = response?.headers.get('Content-Disposition')?.match(/filename="([A-Za-z0-9._-]+)"/)?.[1]
  const verified = response?.headers.get('X-Supermetry-Privacy-Scope') === 'verified-only'
  link.download = filename ?? `${query.dimensions?.[0] ?? 'analytics'}${verified ? '-verified-only' : ''}.csv`
  link.click()
  URL.revokeObjectURL(url)
}
