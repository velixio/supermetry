import {
  firstPage,
  parseDashboardSearch,
  parsePaginationSearch,
  positiveSearchID,
  searchRecord,
  type DashboardSearch,
  type PaginationSearch,
} from '@/lib/search'

export type ReplayLayout = 'table' | 'cards'

export type ReplaySearch = DashboardSearch & PaginationSearch & {
  replay?: string
  q?: string
  min_duration?: number
  max_duration?: number
  has_events?: boolean
  saved?: boolean
  layout?: ReplayLayout
}

function optionalBoundedInteger(value: unknown): number | undefined {
  const parsed = typeof value === 'number' ? value : typeof value === 'string' && value !== '' ? Number(value) : Number.NaN
  return Number.isInteger(parsed) && parsed >= 0 && parsed <= 31_536_000 ? parsed : undefined
}

function optionalBoolean(value: unknown): boolean | undefined {
  if (value === true || value === 'true') return true
  if (value === false || value === 'false') return false
  return undefined
}

export function parseReplaySearch(value: unknown): ReplaySearch {
  const raw = searchRecord(value)
  const replay = positiveSearchID(raw.replay)
  const query = typeof raw.q === 'string' ? raw.q.trim() : ''
  const minDuration = optionalBoundedInteger(raw.min_duration)
  const maxDuration = optionalBoundedInteger(raw.max_duration)
  const hasEvents = optionalBoolean(raw.has_events)
  const saved = optionalBoolean(raw.saved)
  return {
    ...parseDashboardSearch(raw),
    ...parsePaginationSearch(raw),
    ...(replay ? { replay } : {}),
    ...(query && [...query].length <= 200 ? { q: query } : {}),
    ...(minDuration === undefined ? {} : { min_duration: minDuration }),
    ...(maxDuration === undefined ? {} : { max_duration: maxDuration }),
    ...(hasEvents === undefined ? {} : { has_events: hasEvents }),
    ...(saved === undefined ? {} : { saved }),
    ...((raw.layout === 'cards' || raw.layout === 'table') ? { layout: raw.layout } : {}),
  }
}

export function replayFilterSearch(search: ReplaySearch | Record<string, unknown>, patch: Partial<ReplaySearch>): ReplaySearch & { page: number; cursors: string[] } {
  return firstPage({ ...parseReplaySearch(search), ...patch })
}

function replayLayoutKey(userId: number): string {
  return `supermetry:replays:layout:v1:${userId}`
}

export function readReplayLayout(userId: number): ReplayLayout {
  if (typeof window === 'undefined') return 'table'
  try {
    return window.localStorage.getItem(replayLayoutKey(userId)) === 'cards' ? 'cards' : 'table'
  } catch {
    return 'table'
  }
}

export function rememberReplayLayout(userId: number, layout: ReplayLayout): void {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(replayLayoutKey(userId), layout)
  } catch {
    // Browser privacy settings may disable storage; the URL value still works.
  }
}
