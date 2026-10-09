import { describe, expect, it } from 'vitest'
import type { Site } from '@/routes/app.$org'
import { visibleNavigation } from './Sidebar'

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
      expect(visibleNavigation('owner', true, { effective_capabilities: ['profiles.read', 'replay.read'] }, site).map((item) => item.suffix)).toEqual(['', '/sites', '/health'])
    }
  })

  it('preserves identified navigation when project profiles are disabled', () => {
    const identified: Site = { id: 7, name: 'Docs', profiles_enabled: false, tracking_policy: { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }
    const suffixes = visibleNavigation('owner', true, { effective_capabilities: ['profiles.read', 'replay.read'] }, identified).map((item) => item.suffix)
    for (const suffix of ['/events', '/sessions', '/profiles', '/journeys', '/heatmaps', '/vitals', '/funnels', '/realtime', '/goals', '/revenue']) expect(suffixes).toContain(suffix)
  })

})
