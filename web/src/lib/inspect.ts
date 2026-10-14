import type { Filter } from '@/lib/search'
import { isAnonymousDimensionAllowed, isAnonymousFilterAllowed } from '@/lib/tracking-policy'

/** Dimensions the dashboard can inspect: every BreakdownCard tab dimension plus one Weekly rhythm cell. */
export const inspectDimensions = [
  'page', 'entry', 'exit',
  'source', 'channel', 'utm_source', 'utm_medium', 'utm_campaign',
  'country', 'region', 'city', 'language', 'hostname',
  'browser', 'browser_version', 'os', 'os_version', 'device', 'screen_size',
  'goal', 'event',
  'weekday_hour',
] as const

export type InspectDimension = (typeof inspectDimensions)[number]
export type InspectTarget = { dimension: InspectDimension; values: string[] }

/** What a source needs to offer Inspect: the current selection, the privacy check, and the opener. */
export type InspectControl = {
  selected?: InspectTarget
  can(target: InspectTarget): boolean
  open(target: InspectTarget, origin: HTMLElement): void
}

function isInspectDimension(value: string): value is InspectDimension {
  return (inspectDimensions as readonly string[]).includes(value)
}

function validValues(dimension: InspectDimension, values: string[]): boolean {
  if (values.some((value) => value.trim() === '')) return false
  if (dimension === 'weekday_hour') {
    if (values.length !== 2 || !values.every((value) => /^\d{1,2}$/.test(value))) return false
    const [weekday, hour] = values.map(Number)
    return weekday <= 6 && hour <= 23
  }
  // Region and city are scoped to a country, the same [place, ISO country] pair a row filter uses.
  if (dimension === 'region' || dimension === 'city') return values.length === 2 && /^[A-Z]{2}$/.test(values[1])
  return values.length === 1
}

export function inspectTargetFor(dimension: string, values: string[]): InspectTarget | undefined {
  return isInspectDimension(dimension) && validValues(dimension, values) ? { dimension, values: [...values] } : undefined
}

function decode(value: string): string | undefined {
  try {
    return decodeURIComponent(value)
  } catch {
    return undefined
  }
}

/** Reads `<dimension>:<value>[,<value>]`; each value is URI-encoded, as in the `f` filter param. */
export function parseInspect(raw: unknown): InspectTarget | undefined {
  if (typeof raw !== 'string') return undefined
  const separator = raw.indexOf(':')
  if (separator < 1) return undefined
  const values = raw.slice(separator + 1).split(',').map(decode)
  if (values.some((value) => value === undefined)) return undefined
  return inspectTargetFor(raw.slice(0, separator), values as string[])
}

export function serializeInspect(target: InspectTarget): string {
  return `${target.dimension}:${target.values.map((value) => encodeURIComponent(value)).join(',')}`
}

/**
 * The explicit `undefined` matters: TanStack merges the parent match's raw search under a route's validated search,
 * so leaving the key out would let a malformed raw value through to useSearch().
 */
export function parseInspectSearch(raw: unknown): { inspect?: string } {
  const value = raw && typeof raw === 'object' ? (raw as Record<string, unknown>).inspect : undefined
  const target = parseInspect(value)
  return { inspect: target ? serializeInspect(target) : undefined }
}

export function withoutInspect<T extends { inspect?: string }>(search: T): Omit<T, 'inspect'> {
  const copy = { ...search }
  delete copy.inspect
  return copy
}

export function sameInspect(left?: InspectTarget, right?: InspectTarget): boolean {
  return left !== undefined && right !== undefined && left.dimension === right.dimension &&
    left.values.length === right.values.length && left.values.every((value, index) => value === right.values[index])
}

/** Selecting the open value again closes the inspector; anything else replaces it. */
export function nextInspect(current: InspectTarget | undefined, target: InspectTarget): string | undefined {
  return sameInspect(current, target) ? undefined : serializeInspect(target)
}

export function inspectFilter(target: InspectTarget): Filter | undefined {
  if (target.dimension === 'weekday_hour') return undefined
  return { dimension: target.dimension, operator: 'is', values: [...target.values] }
}

/** Anonymous sites inspect only values they could also filter by; a Weekly rhythm cell exposes nothing the grid does not show. */
export function inspectAllowed(target: InspectTarget, anonymous: boolean): boolean {
  if (!anonymous || target.dimension === 'weekday_hour') return true
  const filter = inspectFilter(target)
  return filter !== undefined && isAnonymousDimensionAllowed(target.dimension) && isAnonymousFilterAllowed(filter)
}

export function activeInspectTarget(raw: unknown, context: { anonymous: boolean; standardDashboard: boolean }): InspectTarget | undefined {
  if (!context.standardDashboard) return undefined
  const target = parseInspect(raw)
  return target && inspectAllowed(target, context.anonymous) ? target : undefined
}

export function inspectCrossDimensions(target: InspectTarget, anonymous: boolean): string[] {
  if (target.dimension === 'weekday_hour') return []
  const candidates = anonymous ? ['channel', 'country', 'device'] : ['page', 'source', 'country', 'device']
  return candidates.filter((dimension) =>
    dimension !== target.dimension &&
    !(dimension === 'country' && (target.dimension === 'region' || target.dimension === 'city')) &&
    !(dimension === 'page' && (target.dimension === 'entry' || target.dimension === 'exit')))
}
