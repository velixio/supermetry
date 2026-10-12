import { activeLocale } from '@/lib/i18n/intl'
export const periodValues = [
  'today',
  'yesterday',
  '7d',
  '28d',
  '30d',
  '91d',
  '6mo',
  '12mo',
  'month',
  'last_month',
  'year',
  'custom',
] as const

export const compareValues = [
  'off',
  'previous_period',
  'previous_year',
] as const

export const metricValues = [
  'visitors',
  'visits',
  'pageviews',
  'views_per_visit',
  'bounce_rate',
  'visit_duration',
  'engagement_time',
  'exit_rate',
  'time_on_page',
  'total_revenue',
  'average_revenue',
] as const

export const filterOperatorValues = [
  'is',
  'is_not',
  'contains',
  'starts_with',
] as const

function validDate(value: string): boolean {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value)
  if (!match) return false
  const year = Number(match[1])
  const month = Number(match[2])
  const day = Number(match[3])
  const date = new Date(Date.UTC(year, month - 1, day))
  return (
    date.getUTCFullYear() === year &&
    date.getUTCMonth() === month - 1 &&
    date.getUTCDate() === day
  )
}

type SearchRecord = Record<string, unknown>

export function searchRecord(value: unknown): SearchRecord {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as SearchRecord : {}
}

export function searchString(value: unknown, fallback = ''): string {
  return typeof value === 'string' ? value : fallback
}

export function optionalSearchString(value: unknown): string | undefined {
  return typeof value === 'string' ? value : undefined
}

export function searchEnum<const Values extends readonly string[]>(
  value: unknown,
  values: Values,
  fallback: Values[number],
): Values[number] {
  return typeof value === 'string' && (values as readonly string[]).includes(value)
    ? value as Values[number]
    : fallback
}

export function positiveSearchInteger(value: unknown): number | undefined {
  const number = typeof value === 'number' ? value : typeof value === 'string' && value !== '' ? Number(value) : NaN
  return Number.isInteger(number) && number > 0 ? number : undefined
}

export function boundedSearchInteger(value: unknown, minimum: number, maximum: number, fallback: number): number {
  const number = typeof value === 'number' ? value : typeof value === 'string' && value !== '' ? Number(value) : NaN
  return Number.isInteger(number) && number >= minimum && number <= maximum ? number : fallback
}

export function positiveSearchID(value: unknown): string | undefined {
  return typeof value === 'string' && /^[1-9][0-9]*$/.test(value) ? value : undefined
}

export function searchBoolean(value: unknown, fallback = false): boolean {
  if (value === true || value === 'true') return true
  if (value === false || value === 'false') return false
  return fallback
}

export function parsePaginationSearch(value: unknown): PaginationSearch {
  const search = searchRecord(value)
  const page = positiveSearchInteger(search.page)
  const cursors = Array.isArray(search.cursors) && search.cursors.every((cursor) => typeof cursor === 'string')
    ? search.cursors
    : undefined
  return {
    ...(page === undefined ? {} : { page }),
    ...(cursors === undefined ? {} : { cursors }),
  }
}

export type PaginationSearch = {
  page?: number
  cursors?: string[]
}

export function currentCursor(search: PaginationSearch): string | undefined {
  return search.cursors?.at(-1)
}

export function firstPage<T extends PaginationSearch>(search: T): T & { page: number; cursors: string[] } {
  return { ...search, page: 1, cursors: [] }
}

export function nextPage<T extends PaginationSearch>(search: T, cursor: string): T & { page: number; cursors: string[] } {
  return { ...search, page: (search.page ?? 1) + 1, cursors: [...(search.cursors ?? []), cursor] }
}

export function previousPage<T extends PaginationSearch>(search: T): T & { page: number; cursors: string[] } {
  return {
    ...search,
    page: Math.max(1, (search.page ?? 1) - 1),
    cursors: search.cursors?.slice(0, -1) ?? [],
  }
}

export type Period = (typeof periodValues)[number]
export type Compare = (typeof compareValues)[number]
export type DashboardMetric = (typeof metricValues)[number]
export type DashboardSearch = {
  period: Period
  from?: string
  to?: string
  compare: Compare
  f: string[]
  metric: DashboardMetric
  live?: boolean
  include_imported: boolean
  /** Saved dashboard id, or 'builtin' for the standard dashboard; absent follows the site default. */
  dash?: string
}

function searchDash(value: unknown): string | undefined {
  const text = typeof value === 'number' ? String(value) : typeof value === 'string' ? value : undefined
  return text !== undefined && /^(builtin|[1-9][0-9]{0,11})$/.test(text) ? text : undefined
}

export function parseDashboardSearch(value: unknown): DashboardSearch {
  const search = searchRecord(value)
  const from = optionalSearchString(search.from)
  const to = optionalSearchString(search.to)
  return {
    period: searchEnum(search.period, periodValues, '30d'),
    ...(from && validDate(from) ? { from } : {}),
    ...(to && validDate(to) ? { to } : {}),
    compare: searchEnum(search.compare, compareValues, 'off'),
    f: Array.isArray(search.f) && search.f.every((filter) => typeof filter === 'string') ? search.f : [],
    metric: searchEnum(search.metric, metricValues, 'visitors'),
    ...(search.live === undefined ? {} : { live: searchBoolean(search.live) }),
    include_imported: search.include_imported === undefined || searchBoolean(search.include_imported),
    ...(searchDash(search.dash) ? { dash: searchDash(search.dash) } : {}),
  }
}

export const dashboardSearch = { parse: parseDashboardSearch }
export type FilterOperator = (typeof filterOperatorValues)[number]

export type Filter = {
  dimension: string
  operator: FilterOperator
  values: string[]
}

const dimensions = new Set([
  'browser',
  'browser_version',
  'channel',
  'city',
  'cohort',
  'country',
  'device',
  'entry',
  'event',
  'exit',
  'goal',
  'hostname',
  'language',
  'revenue_currency',
  'os',
  'os_version',
  'page',
  'path',
  'prop',
  'region',
  'source',
  'screen_size',
  'time',
  'utm_campaign',
  'utm_medium',
  'utm_source',
])

function validDimension(dimension: string): boolean {
  return dimensions.has(dimension) || /^prop:[^:]+$/.test(dimension)
}

function decodeValue(value: string): string | undefined {
  try {
    const decoded = decodeURIComponent(value).trim()
    return decoded.length > 0 ? decoded : undefined
  } catch {
    return undefined
  }
}

function parseFilter(raw: string): Filter | undefined {
  for (const operator of filterOperatorValues) {
    const marker = `:${operator}:`
    const markerIndex = raw.indexOf(marker)
    if (markerIndex < 1) continue

    const dimension = raw.slice(0, markerIndex)
    if (!validDimension(dimension)) return undefined

    const encodedValues = raw.slice(markerIndex + marker.length).split(',')
    const values = encodedValues.map(decodeValue)
    if (values.some((value) => value === undefined)) return undefined

    return {
      dimension,
      operator,
      values: values.filter((value): value is string => value !== undefined),
    }
  }
  return undefined
}

export function parseFilters(rawFilters: string[]): {
  filters: Filter[]
  dropped: string[]
} {
  const filters: Filter[] = []
  const dropped: string[] = []
  for (const raw of rawFilters) {
    const filter = parseFilter(raw)
    if (filter) filters.push(filter)
    else dropped.push(raw)
  }
  return { filters, dropped }
}

export function serializeFilter(filter: Filter): string {
  const values = filter.values.map((value) => encodeURIComponent(value)).join(',')
  return `${filter.dimension}:${filter.operator}:${values}`
}

export function serializeFilters(filters: Filter[]): string[] {
  return filters.map(serializeFilter)
}

export function replaceFilters(
  search: DashboardSearch,
  filters: Filter[],
): DashboardSearch {
  return { ...search, f: serializeFilters(filters) }
}

export function addFilter(
  search: DashboardSearch,
  filter: Filter,
): DashboardSearch {
  return { ...search, f: [...search.f, serializeFilter(filter)] }
}

export function removeFilter(
  search: DashboardSearch,
  index: number,
): DashboardSearch {
  if (index < 0 || index >= search.f.length) return { ...search, f: [...search.f] }
  return { ...search, f: search.f.filter((_, current) => current !== index) }
}

function siteDate(timeZone: string, now: Date): string {
  let formatter: Intl.DateTimeFormat
  try {
    formatter = new Intl.DateTimeFormat(activeLocale(), {
      timeZone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    })
  } catch {
    formatter = new Intl.DateTimeFormat(activeLocale(), {
      timeZone: 'UTC',
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    })
  }
  const parts = formatter.formatToParts(now)
  const part = (type: Intl.DateTimeFormatPartTypes) =>
    parts.find((candidate) => candidate.type === type)?.value ?? ''
  return `${part('year')}-${part('month')}-${part('day')}`
}

function civilDate(value: string): Date {
  const [year, month, day] = value.split('-').map(Number)
  return new Date(Date.UTC(year, month - 1, day))
}

function dateString(date: Date): string {
  return `${date.getUTCFullYear().toString().padStart(4, '0')}-${(date.getUTCMonth() + 1).toString().padStart(2, '0')}-${date.getUTCDate().toString().padStart(2, '0')}`
}

function shiftDays(value: string, days: number): string {
  const date = civilDate(value)
  date.setUTCDate(date.getUTCDate() + days)
  return dateString(date)
}

function monthStart(value: string): string {
  const date = civilDate(value)
  date.setUTCDate(1)
  return dateString(date)
}

function shiftMonths(value: string, months: number): string {
  const date = civilDate(value)
  const day = date.getUTCDate()
  date.setUTCDate(1)
  date.setUTCMonth(date.getUTCMonth() + months)
  const lastDay = new Date(
    Date.UTC(date.getUTCFullYear(), date.getUTCMonth() + 1, 0),
  ).getUTCDate()
  date.setUTCDate(Math.min(day, lastDay))
  return dateString(date)
}

function shiftYears(value: string, years: number): string {
  const date = civilDate(value)
  date.setUTCFullYear(date.getUTCFullYear() + years)
  return dateString(date)
}

function fallbackRange(today: string): { from: string; to: string } {
  return { from: shiftDays(today, -29), to: today }
}

export function resolveRange(
  period: Period,
  timeZone: string,
  now: Date,
  from?: string,
  to?: string,
): { from: string; to: string } {
  const today = siteDate(timeZone, now)
  switch (period) {
    case 'today':
      return { from: today, to: today }
    case 'yesterday': {
      const yesterday = shiftDays(today, -1)
      return { from: yesterday, to: yesterday }
    }
    case '7d':
      return { from: shiftDays(today, -6), to: today }
    case '28d':
      return { from: shiftDays(today, -27), to: today }
    case '30d':
      return fallbackRange(today)
    case '91d':
      return { from: shiftDays(today, -90), to: today }
    case '6mo':
      return { from: shiftDays(shiftMonths(today, -6), 1), to: today }
    case '12mo':
      return { from: shiftDays(shiftYears(today, -1), 1), to: today }
    case 'month':
      return { from: monthStart(today), to: today }
    case 'last_month': {
      const thisMonth = monthStart(today)
      return {
        from: shiftMonths(thisMonth, -1),
        to: shiftDays(thisMonth, -1),
      }
    }
    case 'year':
      return { from: `${today.slice(0, 4)}-01-01`, to: today }
    case 'custom':
      if (
        from !== undefined &&
        to !== undefined &&
        validDate(from) &&
        validDate(to) &&
        from <= to
      ) {
        return { from, to }
      }
      return fallbackRange(today)
  }
}
