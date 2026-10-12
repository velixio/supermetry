import { describe, expect, it } from 'vitest'
import type { Site } from '@/lib/me'
import { anonymousRouteRestricted } from './AnalyticsPrivacyBoundary'
import { settingsDestinations, settingsIsActive, visibleNavigation } from './Sidebar'

describe('primary sidebar navigation', () => {
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
      expect(visibleNavigation('owner', true, { effective_capabilities: ['profiles.read', 'replay.read'] }, site).map((item) => item.suffix)).toEqual(['', '/sites', '/links', '/health'])
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
})
