import { resolveRange, type Period } from '@/lib/search'

export const APP_PERIODS: readonly [Period, string][] = [
  ['today', 'Today'], ['7d', 'Last 7 days'], ['28d', 'Last 28 days'], ['30d', 'Last 30 days'], ['91d', 'Last 91 days'], ['6mo', 'Last 6 months'], ['12mo', 'Last 12 months'],
]
export const APP_PLATFORMS = ['windows', 'macos', 'linux', 'ios', 'android', 'web', 'other'] as const
/** Electron process types; the server normalises the SDK's value to one of these. */
export const APP_PROCESSES = ['main', 'renderer', 'gpu', 'utility', 'other'] as const
export const ISSUE_STATUSES = ['open', 'regressed', 'resolved', 'ignored', 'archived', 'all'] as const
export const ISSUE_SORTS = ['last_seen', 'events', 'users'] as const
export const ISSUE_KINDS = ['crash', 'error', 'hang', 'oom', 'watchdog', 'install_failed', 'install_stalled', 'incompatibility', 'operation_failed'] as const

export type IssueStatusTab = (typeof ISSUE_STATUSES)[number]
export type IssueSort = (typeof ISSUE_SORTS)[number]

export type AppSearch = { period: Period; release?: string; environment?: string; platform?: string; process?: string }
export type AppIssuesSearch = AppSearch & { status: IssueStatusTab; kind?: string; q?: string; query?: string; sort: IssueSort; offset?: number }

const text = (value: unknown): string | undefined => (typeof value === 'string' && value.trim() !== '' ? value.trim().slice(0, 200) : undefined)
const oneOf = <T extends string>(list: readonly T[], value: unknown): T | undefined => list.find((item) => item === value)

export function parseAppSearch(search: Record<string, unknown>): AppSearch {
  const period = APP_PERIODS.find(([value]) => value === search.period)?.[0] ?? '30d'
  return { period, release: text(search.release), environment: text(search.environment), platform: oneOf(APP_PLATFORMS, search.platform), process: oneOf(APP_PROCESSES, search.process) }
}

export function parseAppIssuesSearch(search: Record<string, unknown>): AppIssuesSearch {
  const offset = Number(search.offset)
  return {
    ...parseAppSearch(search),
    status: oneOf(ISSUE_STATUSES, search.status) ?? 'open',
    kind: oneOf(ISSUE_KINDS, search.kind),
    q: text(search.q),
    query: text(search.query),
    sort: oneOf(ISSUE_SORTS, search.sort) ?? 'last_seen',
    offset: Number.isInteger(offset) && offset > 0 ? offset : undefined,
  }
}

export function hasAppFilters(search: AppSearch): boolean {
  return Boolean(search.release || search.environment || search.platform || search.process)
}

/** Query string for the app read APIs: the resolved date range plus the active filters. */
export function appQuery(search: AppSearch, timezone: string, extra: Record<string, string | number | undefined> = {}, now = new Date()): URLSearchParams {
  const range = resolveRange(search.period, timezone || 'UTC', now)
  const query = new URLSearchParams({ from: range.from, to: range.to })
  for (const [key, value] of Object.entries({ release: search.release, environment: search.environment, platform: search.platform, ...extra })) {
    if (value !== undefined && value !== '') query.set(key, String(value))
  }
  return query
}

/** The app filters as extra search keys (only the ones that are set), for routes whose own schema does not know them. */
export function appFilterSearch(search: Record<string, unknown>): { release?: string; environment?: string; platform?: string; process?: string } {
  const { release, environment, platform, process } = parseAppSearch(search)
  return { ...(release ? { release } : {}), ...(environment ? { environment } : {}), ...(platform ? { platform } : {}), ...(process ? { process } : {}) }
}

const compact = <T extends object>(value: T): Partial<T> => Object.fromEntries(Object.entries(value).filter(([, item]) => item !== undefined)) as Partial<T>

/** Route `validateSearch` helpers: keep only the keys that are set so links need not repeat defaults. */
export const appSearchInput = (search: Record<string, unknown>): Partial<AppSearch> => compact(parseAppSearch(search))
export const appIssuesSearchInput = (search: Record<string, unknown>): Partial<AppIssuesSearch> => compact(parseAppIssuesSearch(search))

/** The issue search `query` with the process filter appended as a `process:` term (the issues API has no separate parameter). */
export function issueSearchQuery(search: { query?: string; process?: string }): string | undefined {
  const parts = [search.query?.trim(), search.process ? `process:${search.process}` : undefined].filter(Boolean)
  return parts.length > 0 ? parts.join(' ') : undefined
}
