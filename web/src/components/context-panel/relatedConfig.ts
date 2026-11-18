import { isOrgScopedPath } from '@/components/routeCrumbs'
import { stringifySearch } from '@/lib/search'

/**
 * Where each page leads next. Keys are page keys (see parsePage): site pages by their path under the project
 * ('events', 'settings/replay'), organization pages as 'org/<page>', account pages as 'account/<page>'.
 */
export type PageKey = string

export const RELATED: Record<PageKey, readonly PageKey[]> = {
  dashboard: ['events', 'sessions', 'journeys', 'goals'],
  realtime: ['sessions', 'events', 'replays'],
  sessions: ['replays', 'events', 'journeys'],
  events: ['sessions', 'funnels', 'goals'],
  journeys: ['funnels', 'sessions', 'replays'],
  retention: ['cohorts', 'profiles', 'journeys'],
  profiles: ['cohorts', 'sessions', 'retention'],
  cohorts: ['retention', 'profiles', 'funnels'],
  funnels: ['goals', 'journeys', 'replays'],
  goals: ['funnels', 'events', 'revenue'],
  revenue: ['goals', 'dashboard', 'funnels'],
  replays: ['sessions', 'heatmaps', 'events'],
  heatmaps: ['replays', 'sessions', 'vitals'],
  vitals: ['heatmaps', 'sessions', 'dashboard'],
  links: ['dashboard', 'events'],
  'settings/general': ['settings/installation', 'settings/tracking', 'settings/shares'],
  'settings/installation': ['settings/tracking', 'settings/general', 'settings/integrations'],
  'settings/tracking': ['settings/installation', 'settings/replay', 'settings/general'],
  'settings/replay': ['settings/tracking', 'settings/general', 'org/replay-storage'],
  'settings/goals': ['goals', 'funnels', 'settings/alerts'],
  'settings/funnels': ['funnels', 'settings/goals'],
  'settings/alerts': ['settings/monitors', 'settings/alert-silences', 'settings/notification-history'],
  'settings/alert-silences': ['settings/alerts', 'settings/notification-history'],
  'settings/monitors': ['settings/alerts', 'settings/notification-history'],
  'settings/notification-history': ['settings/alerts', 'settings/alert-silences'],
  'settings/integrations': ['settings/imports', 'settings/installation', 'settings/reports'],
  'settings/imports': ['settings/integrations', 'settings/general'],
  'settings/reports': ['settings/alerts', 'settings/shares', 'settings/integrations'],
  'settings/shares': ['settings/general', 'settings/reports'],
  'settings/symbols': ['settings/installation', 'settings/general'],
  'settings/app': ['settings/installation', 'settings/general'],
  'org/overview': ['org/members', 'org/api-keys', 'org/notifications'],
  'org/members': ['org/invites', 'org/audit', 'org/api-keys'],
  'org/invites': ['org/members', 'org/audit'],
  'org/api-keys': ['org/api', 'org/mcp', 'org/audit'],
  'org/api': ['org/api-keys', 'org/mcp', 'org/exports'],
  'org/mcp': ['org/api-keys', 'org/api'],
  'org/audit': ['org/members', 'org/exports'],
  'org/exports': ['org/audit', 'org/api'],
  'org/notifications': ['org/members', 'org/audit'],
  'org/replay-storage': ['org/overview', 'org/exports'],
  sites: ['health', 'github-stars'],
  health: ['sites', 'github-stars'],
  'github-stars': ['sites', 'health'],
  'account/profile': ['account/security', 'account/notifications', 'account/sessions'],
  'account/security': ['account/sessions', 'account/profile'],
  'account/sessions': ['account/security', 'account/profile'],
  'account/notifications': ['account/profile', 'account/security'],
}

/** Pages whose route reads the period, comparison and filters from the URL, so a link can carry them. */
export const CARRIES_SEARCH: ReadonlySet<PageKey> = new Set([
  'dashboard', 'events', 'sessions', 'journeys', 'retention', 'cohorts', 'heatmaps', 'vitals', 'revenue', 'replays', 'profiles',
])
const CARRIED = ['period', 'from', 'to', 'compare', 'f'] as const

const ORG_PAGES = new Set(['sites', 'health', 'github-stars'])

export type ParsedPage = { org?: string; site?: string; key?: PageKey }

/** The page key, organization and project of a pathname. Pure. */
export function parsePage(pathname: string): ParsedPage {
  const parts = pathname.split('/').filter(Boolean)
  if (parts[0] !== 'app' || parts.length < 2) return {}
  if (parts[1] === 'account') return { key: parts[2] ? `account/${parts[2]}` : 'account/profile' }
  if (parts[1] === 'admin') return {}
  const org = parts[1]
  if (!parts[2]) return { org, key: 'sites' }
  if (isOrgScopedPath(pathname)) {
    if (parts[2] === 'settings') return { org, key: `org/${parts[3] ?? 'overview'}` }
    return { org, key: ORG_PAGES.has(parts[2]) ? parts[2] : undefined }
  }
  const site = parts[2]
  if (!parts[3]) return { org, site, key: 'dashboard' }
  if (parts[3] === 'settings') return { org, site, key: `settings/${parts[4] ?? 'general'}` }
  return { org, site, key: parts[3] }
}

/** The path of a page key, or undefined when it needs a project and there is none. */
export function pagePath(key: PageKey, org?: string, site?: string): string | undefined {
  if (key.startsWith('account/')) return `/app/${key}`
  if (!org) return undefined
  if (key.startsWith('org/')) {
    const page = key.slice(4)
    return page === 'overview' ? `/app/${org}/settings` : `/app/${org}/settings/${page}`
  }
  if (ORG_PAGES.has(key)) return `/app/${org}/${key}`
  if (!site) return undefined
  return key === 'dashboard' ? `/app/${org}/${site}` : `/app/${org}/${site}/${key}`
}

export type RelatedLink = { key: PageKey; href: string }

/**
 * The next pages worth visiting from `current`. Period, comparison and filters travel along when both ends read them;
 * projects-only targets are dropped when no project is open.
 */
export function relatedLinks(current: ParsedPage, search: Record<string, unknown> = {}): RelatedLink[] {
  if (!current.key) return []
  const carried: Record<string, unknown> = {}
  if (CARRIES_SEARCH.has(current.key)) {
    for (const name of CARRIED) {
      const value = search[name]
      if (value !== undefined && !(Array.isArray(value) && value.length === 0)) carried[name] = value
    }
  }
  const links: RelatedLink[] = []
  for (const key of RELATED[current.key] ?? []) {
    const path = pagePath(key, current.org, current.site)
    if (!path) continue
    const query = CARRIES_SEARCH.has(key) && Object.keys(carried).length > 0 ? stringifySearch(carried) : ''
    links.push({ key, href: path + query })
  }
  return links
}
