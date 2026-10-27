import { describe, expect, it } from 'vitest'
import type { Site } from '@/lib/me'
import { anonymousRouteRestricted } from './AnalyticsPrivacyBoundary'
import { navigationHref, navigationIsActive, settingsDestinations, settingsIsActive, visibleNavigation } from './Sidebar'
import { accountDestinationIsActive, accountDestinations } from './AccountSidebar'

describe('primary sidebar navigation', () => {
  it('adds the organization-level GitHub stars item for every member', () => {
    for (const role of ['owner', 'admin', 'editor', 'viewer'] as const) {
      expect(visibleNavigation(role, false).map((item) => item.suffix)).toContain('/github-stars')
    }
    const items = visibleNavigation('viewer', false)
    const stars = items.find((item) => item.suffix === '/github-stars')
    expect(stars?.label).toBe('GitHub stars')
    expect(stars && navigationHref(stars, 'acme', 'docs')).toBe('/app/acme/github-stars')
    // It needs no site and stays in the menu of pixel projects.
    expect(visibleNavigation('owner', true, undefined, { id: 1, name: 'R', kind: 'pixel' } as Site).map((item) => item.suffix)).toContain('/github-stars')
  })

  it('marks GitHub stars active on its own path only', () => {
    const stars = visibleNavigation('owner', false).find((item) => item.suffix === '/github-stars')!
    expect(navigationIsActive(stars, '/app/acme/github-stars', 'acme', '')).toBe(true)
    expect(navigationIsActive(stars, '/app/acme/health', 'acme', '')).toBe(false)
    expect(navigationIsActive(stars, '/app/github-stars', 'acme', '')).toBe(false)
  })

  it('hides web-only pages for pixel projects', () => {
    const items = visibleNavigation('owner', true, undefined, { id: 1, name: 'R', kind: 'pixel' } as Site)
    expect(items.map((item) => item.suffix)).toEqual(['', '/sites', '/health', '/github-stars'])
  })

  it('keeps settings out of analytics navigation for every role and site state', () => {
    for (const role of ['owner', 'admin', 'editor', 'viewer'] as const) {
      for (const hasSite of [true, false]) {
        expect(visibleNavigation(role, hasSite).some((item) => /settings/i.test(item.label))).toBe(false)
      }
    }
  })
  it('shows only supported analytics navigation for anonymous or missing policy', () => {
    const anonymous: Site = { id: 7, name: 'Docs', tracking_policy: { instance_mode: 'anonymous', effective_mode: 'anonymous', configured_profiles_enabled: true, restrictions: [] } }
    for (const site of [anonymous, undefined]) {
      expect(visibleNavigation('owner', true, { effective_capabilities: ['profiles.read', 'replay.read'] }, site).map((item) => item.suffix)).toEqual(['', '/sites', '/links', '/health', '/github-stars'])
    }
  })

  it('preserves identified navigation when project profiles are disabled', () => {
    const identified: Site = { id: 7, name: 'Docs', profiles_enabled: false, tracking_policy: { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }
    const suffixes = visibleNavigation('owner', true, { effective_capabilities: ['profiles.read', 'replay.read'] }, identified).map((item) => item.suffix)
    for (const suffix of ['/events', '/sessions', '/profiles', '/journeys', '/heatmaps', '/vitals', '/funnels', '/realtime', '/goals', '/revenue']) expect(suffixes).toContain(suffix)
    expect(suffixes).not.toContain('/map')
  })

  it('offers site settings to every role but viewers, and organisation settings to all', () => {
    const site: Site = { id: 7, name: 'Docs', slug: 'docs' }
    for (const role of ['owner', 'admin', 'editor'] as const) expect(settingsDestinations(role, site)).toEqual(['site', 'org'])
    expect(settingsDestinations('viewer', site)).toEqual(['org'])
    expect(settingsDestinations('owner', undefined)).toEqual(['org'])
  })

  it('marks the settings destination active for its own section, by any site alias', () => {
    const site: Site = { id: 7, name: 'Docs', slug: 'docs', public_id: 'docs-public' }
    expect(settingsIsActive('org', '/app/acme/settings/members', 'acme', site)).toBe(true)
    expect(settingsIsActive('org', '/app/acme/docs/settings/general', 'acme', site)).toBe(false)
    expect(settingsIsActive('org', '/app/other/settings', 'acme', site)).toBe(false)
    for (const alias of ['docs', 'docs-public', '7']) {
      expect(settingsIsActive('site', `/app/acme/${alias}/settings/general`, 'acme', site)).toBe(true)
    }
    expect(settingsIsActive('site', '/app/acme/docs', 'acme', site)).toBe(false)
    expect(settingsIsActive('site', '/app/acme/settings', 'acme', site)).toBe(false)
    expect(settingsIsActive('site', '/app/acme/docs/settings/general', 'acme', undefined)).toBe(false)
  })

  it('links site settings to a page that anonymous tracking does not block', () => {
    expect(anonymousRouteRestricted('/app/acme/docs/settings/general')).toBe(false)
  })

  it('offers account settings to everyone and instance administration only to instance admins', () => {
    const user = { id: 1, name: 'Owner', email: 'owner@example.test' }
    expect(accountDestinations({ user, memberships: [] })).toEqual(['account'])
    expect(accountDestinations({ user: { ...user, instance_admin: false }, memberships: [] })).toEqual(['account'])
    expect(accountDestinations({ user: { ...user, instance_admin: true }, memberships: [] })).toEqual(['account', 'admin'])
  })

  it('marks an account destination active only inside its own section', () => {
    for (const path of ['/app/account/profile', '/app/account/security', '/app/account/sessions']) {
      expect(accountDestinationIsActive('account', path)).toBe(true)
      expect(accountDestinationIsActive('admin', path)).toBe(false)
    }
    expect(accountDestinationIsActive('account', '/app/account/organizations/new')).toBe(false)
    expect(accountDestinationIsActive('account', '/app/accounting')).toBe(false)
    expect(accountDestinationIsActive('admin', '/app/admin/users')).toBe(true)
    expect(accountDestinationIsActive('admin', '/app/admin')).toBe(true)
    expect(accountDestinationIsActive('admin', '/app/administrator')).toBe(false)
    expect(accountDestinationIsActive('admin', '/app/acme/settings')).toBe(false)
  })
})
