import { siteSlug } from '@/lib/matchSite'
import { orgSwitchHref, siteSwitchHref, type CurrentRoute } from '@/lib/switchTarget'
import type { Me, Membership } from '@/lib/me'
import type { Theme } from '@/lib/theme'
import { en } from '@/messages/en'
import { ui } from '@/messages/ui'

import { accountDestinations } from './AccountSidebar'
import {
  activeSite,
  canCreateSites,
  githubStarsPath,
  navigationUrl,
  organizationSettingsHref,
  replaysAvailable,
  replaysUrl,
  settingsDestinations,
  siteSettingsHref,
  visibleNavigation,
} from './Sidebar'

export const paletteGroups = ['recent', 'pages', 'settings', 'actions', 'sites', 'organizations'] as const
export type PaletteGroup = (typeof paletteGroups)[number]
export type PaletteAction = 'theme' | 'help' | 'about' | 'logout'

type PaletteBase = { id: string; group: PaletteGroup; label: string; detail?: string; keywords: string[] }
export type PaletteCommand =
  | (PaletteBase & { kind: 'navigate'; href: string })
  | (PaletteBase & { kind: 'action'; action: PaletteAction })

const humanize = (segment: string) => {
  const words = segment.replace(/[-_]+/g, ' ').trim()
  return words.charAt(0).toUpperCase() + words.slice(1)
}

/**
 * Settings destinations read from the router's route tree (full paths such as `/app/$org/settings/members`),
 * so a settings tab added as a route shows up here without touching this file. Routes with other dynamic
 * segments, `new` routes and index layouts are skipped.
 */
export function settingsCommandsFromRoutes(
  routePaths: readonly string[],
  scope: { org?: string; site?: string; canSite: boolean; instanceAdmin: boolean },
): PaletteCommand[] {
  const out: PaletteCommand[] = []
  const seen = new Set<string>()
  for (const raw of routePaths) {
    const parts = raw.split('/').filter(Boolean)
    if (parts[0] !== 'app') continue
    let kind: 'org' | 'site' | 'account' | 'admin'
    let tail: string[]
    let prefix: string
    if (parts[1] === 'account') { kind = 'account'; tail = parts.slice(2); prefix = '/app/account' }
    else if (parts[1] === 'admin') { kind = 'admin'; tail = parts.slice(2); prefix = '/app/admin' }
    else if (parts[1] === '$org' && parts[2] === 'settings') {
      if (!scope.org) continue
      kind = 'org'; tail = parts.slice(3); prefix = `/app/${scope.org}/settings`
    } else if (parts[1] === '$org' && parts[2] === '$site' && parts[3] === 'settings') {
      if (!scope.org || !scope.site || !scope.canSite) continue
      kind = 'site'; tail = parts.slice(4); prefix = `/app/${scope.org}/${scope.site}/settings`
    } else continue
    if (kind === 'admin' && !scope.instanceAdmin) continue
    if (tail.length === 0 || tail.some((segment) => segment.startsWith('$') || segment === 'new')) continue
    const href = `${prefix}/${tail.join('/')}`
    if (seen.has(href)) continue
    seen.add(href)
    const names = tail.map((segment) => ui.crumbs.segments[segment] ?? humanize(segment))
    const label = `${ui.palette.scopes[kind]}: ${names.join(' · ')}`
    out.push({ id: `settings:${href}`, group: 'settings', label, keywords: [label, ...names, ui.menus.settings], kind: 'navigate', href })
  }
  return out
}

/** Moves the commands the user ran lately into the "recent" group, most recent first. */
export function withRecent(commands: PaletteCommand[], recentIds: readonly string[]): PaletteCommand[] {
  const byId = new Map(commands.map((command) => [command.id, command]))
  const recent = recentIds.flatMap((id) => {
    const command = byId.get(id)
    return command ? [{ ...command, group: 'recent' as const }] : []
  })
  const recentSet = new Set(recent.map((command) => command.id))
  return [...recent, ...commands.filter((command) => !recentSet.has(command.id))]
}

/**
 * The command palette entries: exactly what the sidebar, its switcher, the account menu and the
 * header already offer, with the same role, capability and anonymous-tracking gating. Ids are
 * unique because cmdk uses them as item values.
 */
export function paletteCommands({
  me,
  membership,
  requestedSite,
  theme,
  current,
  managed = false,
  routePaths = [],
  recent = [],
}: {
  me: Me
  membership?: Membership
  requestedSite?: string
  theme: Theme
  /** The matched route; switching site or organization keeps this page, like the sidebar switchers. */
  current?: CurrentRoute
  /** The instance is run by an operator, so the instance administration is not offered. */
  managed?: boolean
  /** Full route paths from the router's route tree; settings destinations are derived from them. */
  routePaths?: readonly string[]
  /** Ids of recently run commands, shown first. */
  recent?: readonly string[]
}): PaletteCommand[] {
  const commands: PaletteCommand[] = []
  const page = (id: string, label: string, href: string) =>
    commands.push({ id: `page:${id}`, group: 'pages', label, keywords: [label], kind: 'navigate', href })

  if (membership) {
    const org = membership.org.slug
    const site = activeSite(membership, requestedSite)
    const slug = site ? siteSlug(site) : ''
    const showReplays = replaysAvailable(membership, site)
    for (const item of visibleNavigation(membership.role, Boolean(site), membership, site)) {
      page(item.suffix || '/', item.label, navigationUrl(item, org, slug))
      if (item.suffix === '/realtime' && showReplays) page('/replays', ui.crumbs.segments.replays, replaysUrl(org, slug))
    }
    for (const destination of settingsDestinations(membership.role, site)) {
      if (destination === 'site' && site) page('site-settings', ui.menus.siteSettings, siteSettingsHref(org, slug))
      if (destination === 'org') page('org-settings', ui.menus.organizationSettings, organizationSettingsHref(org))
    }
    for (const other of membership.sites) {
      if (other.id === site?.id) continue
      commands.push({
        id: `site:${other.id}`,
        group: 'sites',
        label: other.name,
        detail: membership.org.name,
        keywords: [other.name, membership.org.name],
        kind: 'navigate',
        href: siteSwitchHref(current, membership, other),
      })
    }
  }

  for (const destination of accountDestinations(me, managed)) {
    if (destination === 'admin') page('admin', en.admin.link, '/app/admin/users')
    else page('account', ui.menus.accountSettings, '/app/account/profile')
  }

  // Inside an organization the item comes from the navigation above; elsewhere /app/github-stars leads to it.
  if (me.memberships.length > 0 && !membership) page('github-stars', en.githubStars.nav, githubStarsPath)

  for (const other of me.memberships) {
    if (other.org.id === membership?.org.id) continue
    commands.push({ id: `org:${other.org.id}`, group: 'organizations', label: other.org.name, keywords: [other.org.name], kind: 'navigate', href: orgSwitchHref(current, other) })
  }

  const site = membership ? activeSite(membership, requestedSite) : undefined
  commands.push(...settingsCommandsFromRoutes(routePaths, {
    org: membership?.org.slug,
    site: site ? siteSlug(site) : undefined,
    canSite: Boolean(membership && site && membership.role !== 'viewer'),
    instanceAdmin: accountDestinations(me, managed).includes('admin'),
  }).filter((command) => !commands.some((existing) => existing.kind === 'navigate' && command.kind === 'navigate' && existing.href === command.href)))

  const link = (id: string, label: string, href: string, keywords: string[] = []) =>
    commands.push({ id: `action:${id}`, group: 'actions', label, keywords: [label, ...keywords], kind: 'navigate', href })
  if (membership) {
    const org = membership.org.slug
    if (membership.role === 'owner' || membership.role === 'admin') {
      link('create-api-key', ui.palette.actions.createApiKey, `/app/${org}/settings/api-keys?create=1`, ['token', 'new key'])
      link('invite-member', ui.palette.actions.inviteMember, `/app/${org}/settings/invites?invite=1`, ['teammate', 'invitation'])
    }
    if (canCreateSites(membership.role)) link('add-site', ui.palette.actions.addSite, `/app/${org}/sites/new`, ['project', 'website'])
  }
  link('language', ui.palette.actions.changeLanguage, '/app/account/profile', ['locale', 'translation'])

  const actions: [PaletteAction, string, string[]][] = [
    ['theme', ui.palette.actions.switchTheme, [theme === 'dark' ? en.theme.dark : en.theme.light, 'dark mode', 'light mode']],
    ['help', en.shortcuts.open, []],
    ['about', ui.palette.actions.about, ['version', 'credits', 'maxmind']],
    ['logout', ui.menus.logOut, ['sign out']],
  ]
  for (const [action, label, keywords] of actions) commands.push({ id: `action:${action}`, group: 'actions', label, keywords: [label, ...keywords], kind: 'action', action })

  return withRecent(commands, recent)
}
