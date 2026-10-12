import { api } from '@/lib/api'
import { queryBody } from '@/lib/csv'
import type { StatsQuery } from '@/lib/stats'

const pageSize = 1000
const maxPages = 200

function slugFromLocation(siteId: number): string {
  const parts = window.location.pathname.split('/')
  const slug = parts[1] === 'app' ? parts[3] : ''
  return slug && /^[A-Za-z0-9._-]+$/.test(slug) ? slug : `site-${siteId}`
}

export function jsonFilename(query: StatsQuery, dimension: string): string {
  const part = (value: string) => value.replace(/[^A-Za-z0-9._-]/g, '_')
  return `${part(slugFromLocation(query.siteId))}-${part(dimension)}-${query.dateRange.from}-${query.dateRange.to}.json`
}

/** Pages through the existing query endpoint so JSON covers every row, not only one page. */
export async function fetchAllRows(query: StatsQuery): Promise<Record<string, unknown>[]> {
  const rows: Record<string, unknown>[] = []
  let cursor: string | undefined
  for (let page = 0; page < maxPages; page += 1) {
    const { data, error } = await api.POST('/api/private/query', {
      body: queryBody({ ...query, limit: pageSize, cursor }),
    })
    if (error) throw error
    if (!data || typeof data === 'string') throw new Error('empty response')
    rows.push(...((data.rows ?? []) as Record<string, unknown>[]))
    cursor = data.next_cursor ?? undefined
    if (!cursor) return rows
  }
  return rows
}

export async function downloadStatsJSON(query: StatsQuery, dimension: string, columns: string[]): Promise<string> {
  const rows = await fetchAllRows(query)
  const picked = rows.map((row) => Object.fromEntries(columns.map((column) => [column, row[column] ?? null])))
  const body = {
    site_id: query.siteId,
    date_range: { ...query.dateRange },
    filters: query.filters.map((filter) => ({ dimension: filter.dimension, operator: filter.operator, values: [...filter.values] })),
    rows: picked,
  }
  const name = jsonFilename(query, dimension)
  const url = URL.createObjectURL(new Blob([JSON.stringify(body, null, 2)], { type: 'application/json' }))
  const link = document.createElement('a')
  link.href = url
  link.download = name
  link.click()
  URL.revokeObjectURL(url)
  return name
}
