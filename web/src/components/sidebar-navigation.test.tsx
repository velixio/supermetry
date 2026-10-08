import { describe, expect, it } from 'vitest'

import { navigationHref, navigationIsActive, visibleNavigation } from './Sidebar'

describe('sidebar navigation', () => {
  it('links owners and admins to organization settings', () => {
    for (const role of ['owner', 'admin'] as const) {
      const item = visibleNavigation(role).find((entry) => entry.label === 'Organization settings')
      expect(item, role).toBeDefined()
      expect(navigationHref(item!, 'acme', 'blog')).toBe('/acme/settings/members')
      expect(navigationIsActive(item!, '/acme/settings/api-keys', 'acme', 'blog')).toBe(true)
      expect(navigationIsActive(item!, '/acme/blog/settings/general', 'acme', 'blog')).toBe(false)
    }
  })

  it('hides organization settings from editors and viewers', () => {
    for (const role of ['editor', 'viewer'] as const) {
      expect(visibleNavigation(role).some((entry) => entry.label === 'Organization settings')).toBe(false)
    }
  })

  it('keeps organization settings reachable without a site', () => {
    expect(visibleNavigation('owner', false).some((entry) => entry.label === 'Organization settings')).toBe(true)
  })

  it('labels the per-site settings entry as site settings', () => {
    expect(visibleNavigation('owner').some((entry) => entry.label === 'Site settings')).toBe(true)
  })
})
