import { describe, expect, it } from 'vitest'

import type { Me, Membership, Site } from '@/lib/me'
import { defaultSearch, stringifySearch } from '@/lib/search'

import { anonymousRouteRestricted } from './AnalyticsPrivacyBoundary'
import { paletteCommands, type PaletteCommand } from './paletteCommands'

const docs: Site = { id: 7, name: 'Docs', slug: 'docs', public_id: 'docs-public', tracking_policy: { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }
const shop: Site = { id: 8, name: 'Shop', slug: 'shop', tracking_policy: { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }
const acme: Membership = { org: { id: 1, name: 'Acme', slug: 'acme' }, role: 'owner', sites: [docs, shop], effective_capabilities: ['profiles.read', 'replay.read'] }
const other: Membership = { org: { id: 2, name: 'Other', slug: 'other' }, role: 'viewer', sites: [] }
const me: Me = { user: { id: 1, name: 'Owner', email: 'owner@example.test' }, memberships: [acme, other] }

const base = stringifySearch(defaultSearch())
const run = (input: Partial<Parameters<typeof paletteCommands>[0]> = {}) =>
  paletteCommands({ me, membership: acme, requestedSite: 'docs', theme: 'light', ...input })
const labels = (commands: PaletteCommand[], group: PaletteCommand['group']) =>
  commands.filter((command) => command.group === group).map((command) => command.label)
const hrefOf = (commands: PaletteCommand[], label: string) => {
  const command = commands.find((item) => item.label === label)
  return command?.kind === 'navigate' ? command.href : undefined
}

describe('paletteCommands', () => {
  it('mirrors the sidebar, settings and account menu for an owner on an identified site', () => {
    const commands = run()
    expect(labels(commands, 'pages')).toEqual([
      'Dashboard', 'Events', 'Sessions', 'Realtime', 'Replays', 'Journeys', 'Retention', 'Profiles', 'Cohorts',
      'Heatmaps', 'Web Vitals', 'Funnels', 'Goals', 'Revenue', 'Sites', 'Links and pixels', 'Data health',
      'Site settings', 'Organization settings', 'Account settings',
    ])
    expect(hrefOf(commands, 'Dashboard')).toBe(`/app/acme/docs${base}`)
    expect(hrefOf(commands, 'Events')).toBe(`/app/acme/docs/events${base}&name=&property=`)
    expect(hrefOf(commands, 'Replays')).toBe(`/app/acme/docs/replays${base}`)
    expect(hrefOf(commands, 'Links and pixels')).toBe('/app/acme/docs/links')
    expect(hrefOf(commands, 'Sites')).toBe('/app/acme/sites')
    expect(hrefOf(commands, 'Data health')).toBe('/app/acme/health')
    expect(hrefOf(commands, 'Site settings')).toBe('/app/acme/docs/settings/general')
    expect(hrefOf(commands, 'Organization settings')).toBe('/app/acme/settings')
    expect(hrefOf(commands, 'Account settings')).toBe('/app/account/profile')
  })

  it('gates pages by role and capability exactly like the sidebar', () => {
    const pages = labels(run({ membership: { ...acme, role: 'viewer', effective_capabilities: [] } }), 'pages')
    for (const hidden of ['Data health', 'Site settings', 'Profiles', 'Cohorts', 'Replays']) expect(pages).not.toContain(hidden)
    expect(pages).toContain('Organization settings')
    expect(pages).toContain('Events')
  })

  it('never offers a screen that anonymous tracking blocks', () => {
    const anonymous: Membership = {
      ...acme,
      sites: acme.sites.map((site) => ({ ...site, tracking_policy: { instance_mode: 'anonymous', effective_mode: 'anonymous', configured_profiles_enabled: true, restrictions: ['instance_anonymous'] } })),
    }
    const commands = run({ membership: anonymous })
    expect(labels(commands, 'pages')).toEqual(['Dashboard', 'Sites', 'Links and pixels', 'Data health', 'Site settings', 'Organization settings', 'Account settings'])
    for (const command of commands) {
      if (command.kind === 'navigate') expect(anonymousRouteRestricted(command.href), command.href).toBe(false)
    }
  })

  it('switches to the other sites of this organization and to other organizations', () => {
    const commands = run()
    expect(commands.filter((command) => command.group === 'sites')).toEqual([
      { id: 'site:8', group: 'sites', label: 'Shop', detail: 'Acme', keywords: ['Shop', 'Acme'], kind: 'navigate', href: `/app/acme/shop${base}` },
    ])
    expect(commands.filter((command) => command.group === 'organizations')).toEqual([
      { id: 'org:2', group: 'organizations', label: 'Other', keywords: ['Other'], kind: 'navigate', href: '/app/other/settings' },
    ])
  })

  it.each(['docs', 'docs-public', '7'])('recognises the current site by the %s alias and links its canonical slug', (alias) => {
    const commands = run({ requestedSite: alias })
    expect(labels(commands, 'sites')).toEqual(['Shop'])
    expect(hrefOf(commands, 'Events')).toBe(`/app/acme/docs/events${base}&name=&property=`)
  })

  it('offers account pages and every organization outside an organization', () => {
    const commands = run({ membership: undefined, requestedSite: undefined })
    expect(labels(commands, 'pages')).toEqual(['Account settings'])
    expect(labels(commands, 'sites')).toEqual([])
    expect(commands.filter((command) => command.group === 'organizations').map((command) => command.kind === 'navigate' && command.href)).toEqual([`/app/acme/docs${base}`, '/app/other/settings'])
    const admin = run({ me: { ...me, user: { ...me.user, instance_admin: true } }, membership: undefined })
    expect(labels(admin, 'pages')).toEqual(['Account settings', 'Instance administration', 'GitHub stars'])
    expect(hrefOf(admin, 'Instance administration')).toBe('/app/admin/users')
  })

  it('offers only existing actions, labels theme for the current theme, and keeps ids unique', () => {
    expect(labels(run(), 'actions')).toEqual(['Use dark theme', 'Show keyboard shortcuts', 'Log out'])
    expect(labels(run({ theme: 'dark' }), 'actions')[0]).toBe('Use light theme')
    const ids = run().map((command) => command.id)
    expect(new Set(ids).size).toBe(ids.length)
  })
})
