import { buildCrumbs } from '@/components/routeCrumbs'
import type { Membership } from '@/lib/me'

import { parsePage } from './relatedConfig'
import { readJson, writeJson } from './storage'

export type RecentPage = { path: string; href: string; label: string; org: string; site?: string }

export const RECENT_SHOWN = 8
const RECENT_STORED = 40
const recentKey = (userId: number | string) => `sm.contextPanel.recent.v1.${userId}`

function isRecent(value: unknown): value is RecentPage {
  if (!value || typeof value !== 'object') return false
  const item = value as Record<string, unknown>
  return typeof item.path === 'string' && typeof item.href === 'string' && typeof item.label === 'string' && typeof item.org === 'string'
}

export function readRecents(userId: number | string): RecentPage[] {
  const stored = readJson(recentKey(userId))
  return Array.isArray(stored) ? stored.filter(isRecent) : []
}

/** The label a visited page gets: its last two breadcrumbs ("Settings / General"), or undefined for pages without any. */
export function recentLabel(pathname: string, membership?: Membership): string | undefined {
  const labels = buildCrumbs(pathname, membership).map((crumb) => crumb.label)
  return labels.length > 0 ? labels.slice(-2).join(' / ') : undefined
}

/** Puts a visit first, replacing an earlier visit of the same path. Returns the stored list. */
export function recordRecent(userId: number | string, pathname: string, searchStr: string, membership?: Membership): RecentPage[] {
  const { org, site } = parsePage(pathname)
  const label = recentLabel(pathname, membership)
  // Account pages are not tied to an organization; they stay out of the per-organization list.
  if (!org || !label) return readRecents(userId)
  const entry: RecentPage = { path: pathname, href: pathname + searchStr, label, org, ...(site && { site }) }
  const next = [entry, ...readRecents(userId).filter((item) => item.path !== pathname)].slice(0, RECENT_STORED)
  writeJson(recentKey(userId), next)
  return next
}

/** Recent pages of this organization and project (organization pages show everywhere), without the page being viewed. */
export function recentsFor(all: RecentPage[], org: string | undefined, site: string | undefined, currentPath: string): RecentPage[] {
  return all.filter((item) => item.org === org && (!item.site || item.site === site) && item.path !== currentPath).slice(0, RECENT_SHOWN)
}
