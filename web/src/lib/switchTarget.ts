import { anonymousRouteRestricted } from '@/components/AnalyticsPrivacyBoundary'
import { hasCapability } from '@/lib/authorization'
import { siteSlug } from '@/lib/matchSite'
import type { Membership, Site } from '@/lib/me'
import { defaultSearch, stringifySearch } from '@/lib/search'
import { isAnonymousTracking } from '@/lib/tracking-policy'

/** The current leaf route, as the router matched it. */
export type CurrentRoute = {
  /** The matched route id, for example "/app/$org/$site/funnels/$id" or "/app/$org/$site/". */
  routeId: string
  search: Record<string, unknown>
}

const SITE_ROOT = '/app/$org/$site'
const ORG_ROOT = '/app/$org'

/** Pages about one entity of the old site fall back to their list on the new one. */
const entityParents: Record<string, string> = {
  '/app/$org/$site/funnels/$id': '/app/$org/$site/funnels',
  '/app/$org/$site/profiles/$id': '/app/$org/$site/profiles',
  '/app/$org/$site/replays/$id': '/app/$org/$site/replays',
  '/app/$org/$site/links/$id': '/app/$org/$site/links',
  '/app/$org/$site/links/pixel_/$id': '/app/$org/$site/links/pixel',
  '/app/$org/github-stars/$owner/$name': '/app/$org/github-stars',
}

/** Search params that mean the same on every site. */
const portableSearch = ['period', 'from', 'to', 'compare', 'granularity', 'metric', 'include_imported'] as const

/** Filter dimensions whose values name something that belongs to one site. */
const siteSpecificFilters = new Set(['goal', 'segment', 'cohort', 'funnel'])

function normalize(routeId: string): string {
  const trimmed = routeId.length > 1 && routeId.endsWith('/') ? routeId.slice(0, -1) : routeId
  return entityParents[trimmed] ?? trimmed
}

/** The site-independent part of the current search, ready for another site. */
export function portableSearchParams(search: Record<string, unknown>): Record<string, unknown> {
  const kept: Record<string, unknown> = {}
  for (const key of portableSearch) {
    if (search[key] !== undefined) kept[key] = search[key]
  }
  if (Array.isArray(search.f)) {
    const filters = search.f.filter((filter): filter is string =>
      typeof filter === 'string' && !siteSpecificFilters.has(filter.split(':')[0] ?? ''))
    if (filters.length > 0) kept.f = filters
  }
  return kept
}

function fill(pattern: string, org: string, site?: string): string {
  return pattern.replace('$org', encodeURIComponent(org)).replace('$site', encodeURIComponent(site ?? ''))
}

/** Whether `pattern` (a site-level route) can be shown for `site` to this member. */
function siteRouteAvailable(pattern: string, membership: Membership, site: Site): boolean {
  if (pattern === SITE_ROOT) return true
  if (site.kind === 'pixel') return false
  const section = pattern.slice(SITE_ROOT.length + 1).split('/')[0] ?? ''
  if (section === 'settings' && membership.role === 'viewer') return false
  if (isAnonymousTracking(site) && anonymousRouteRestricted(fill(pattern, membership.org.slug, siteSlug(site)))) return false
  if ((section === 'profiles' || section === 'cohorts') && !hasCapability(membership, 'profiles.read')) return false
  if (section === 'replays' && (isAnonymousTracking(site) || !hasCapability(membership, 'replay.read'))) return false
  return true
}

/** A navigation target: a concrete pathname and its search params. */
export type SwitchTarget = { pathname: string; search: Record<string, unknown> }

export const targetHref = (target: SwitchTarget) => target.pathname + stringifySearch(target.search)

function siteDashboard(membership: Membership, site: Site, search: Record<string, unknown>): SwitchTarget {
  return { pathname: fill(SITE_ROOT, membership.org.slug, siteSlug(site)), search: { ...defaultSearch(), ...search } }
}

/**
 * Where the site switcher leads: the same page for the new site, with the search params that
 * still make sense there. Entity pages fall back to their list; a page the site or member cannot
 * use falls back to the site's dashboard.
 */
export function siteSwitchTarget(current: CurrentRoute | undefined, membership: Membership, site: Site): SwitchTarget {
  const search = current ? portableSearchParams(current.search) : {}
  const pattern = current ? normalize(current.routeId) : ''
  if (pattern === SITE_ROOT || !pattern.startsWith(`${SITE_ROOT}/`) || !siteRouteAvailable(pattern, membership, site)) {
    return siteDashboard(membership, site, search)
  }
  return { pathname: fill(pattern, membership.org.slug, siteSlug(site)), search }
}

export function siteSwitchHref(current: CurrentRoute | undefined, membership: Membership, site: Site): string {
  return targetHref(siteSwitchTarget(current, membership, site))
}

/** The organization's home: its first site's dashboard, or its settings when it has no site. */
export function organizationHome(membership: Membership): SwitchTarget {
  const site = membership.sites[0]
  return site ? siteDashboard(membership, site, {}) : { pathname: `/app/${encodeURIComponent(membership.org.slug)}/settings`, search: {} }
}

/**
 * Where the organization switcher leads: the same organization-level page (settings, sites,
 * health…) in the new organization, else the organization's home.
 */
export function orgSwitchTarget(current: CurrentRoute | undefined, membership: Membership): SwitchTarget {
  const pattern = current ? normalize(current.routeId) : ''
  if (pattern.startsWith(`${ORG_ROOT}/`) && pattern !== SITE_ROOT && !pattern.startsWith(`${SITE_ROOT}/`)) {
    return { pathname: fill(pattern, membership.org.slug), search: {} }
  }
  return organizationHome(membership)
}

export function orgSwitchHref(current: CurrentRoute | undefined, membership: Membership): string {
  return targetHref(orgSwitchTarget(current, membership))
}
