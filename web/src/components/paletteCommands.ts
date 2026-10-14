import { siteSlug } from '@/lib/matchSite'
import type { Me, Membership } from '@/lib/me'
import type { Theme } from '@/lib/theme'
import { en } from '@/messages/en'
import { ui } from '@/messages/ui'

import { accountDestinations } from './AccountSidebar'
import {
  activeSite,
  navigationUrl,
  organizationUrl,
  organizationSettingsHref,
  replaysAvailable,
  replaysUrl,
  settingsDestinations,
  siteDashboardUrl,
  siteSettingsHref,
  visibleNavigation,
} from './Sidebar'

export const paletteGroups = ['pages', 'sites', 'organizations', 'actions'] as const
export type PaletteGroup = (typeof paletteGroups)[number]
export type PaletteAction = 'theme' | 'help' | 'logout'

type PaletteBase = { id: string; group: PaletteGroup; label: string; detail?: string; keywords: string[] }
export type PaletteCommand =
  | (PaletteBase & { kind: 'navigate'; href: string })
  | (PaletteBase & { kind: 'action'; action: PaletteAction })

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
}: {
  me: Me
  membership?: Membership
  requestedSite?: string
  theme: Theme
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
        href: siteDashboardUrl(org, siteSlug(other)),
      })
    }
  }

  for (const destination of accountDestinations(me)) {
    if (destination === 'admin') page('admin', en.admin.link, '/app/admin/users')
    else page('account', ui.menus.accountSettings, '/app/account/profile')
  }

  for (const other of me.memberships) {
    if (other.org.id === membership?.org.id) continue
    commands.push({ id: `org:${other.org.id}`, group: 'organizations', label: other.org.name, keywords: [other.org.name], kind: 'navigate', href: organizationUrl(other) })
  }

  const actions: [PaletteAction, string][] = [
    ['theme', theme === 'dark' ? en.theme.dark : en.theme.light],
    ['help', en.shortcuts.open],
    ['logout', ui.menus.logOut],
  ]
  for (const [action, label] of actions) commands.push({ id: `action:${action}`, group: 'actions', label, keywords: [label], kind: 'action', action })

  return commands
}
