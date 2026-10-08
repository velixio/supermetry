import { describe, expect, it } from 'vitest'

import {
  canCreateSites,
  loginRedirect,
  navigationHref,
  organizationHref,
  visibleNavigation,
} from './$org'

describe('organization shell authentication', () => {
  it('preserves the full dashboard URL when authentication expires', () => {
    expect(loginRedirect('/acme/blog', '?period=30d')).toBe(
      '/login?next=%2Facme%2Fblog%3Fperiod%3D30d',
    )
  })

  it('hides management navigation from viewers', () => {
    const labels = visibleNavigation('viewer').map((item) => item.label)
    expect(labels).toContain('Dashboard')
    expect(labels).not.toContain('Settings')
    expect(labels).not.toContain('Data health')
  })

  it('switches organizations through the first allowed site', () => {
    expect(
      organizationHref({
        org: { id: 2, name: 'Other', slug: 'other' },
        role: 'viewer',
        sites: [{ id: 8, name: 'Docs', public_id: 'docs-public' }],
      }),
    ).toBe('/other/docs-public')
  })

  it('links data health at organization scope', () => {
    const health = visibleNavigation('owner').find(
      (item) => item.label === 'Data health',
    )
    expect(health && navigationHref(health, 'acme', 'blog')).toBe('/acme/health')
  })

  it('links Web Vitals at site scope', () => {
    const vitals = visibleNavigation('viewer').find(
      (item) => item.label === 'Web Vitals',
    )
    expect(vitals && navigationHref(vitals, 'acme', 'blog')).toBe('/acme/blog/vitals')
  })

  it('links Heatmaps at site scope for every site role', () => {
    for (const role of ['owner', 'admin', 'editor', 'viewer'] as const) {
      const heatmaps = visibleNavigation(role).find((item) => item.label === 'Heatmaps')
      expect(heatmaps && navigationHref(heatmaps, 'acme', 'blog')).toBe('/acme/blog/heatmaps')
    }
  })

  it('shows the organization Sites page to every role', () => {
    for (const role of ['owner', 'admin', 'editor', 'viewer'] as const) {
      expect(visibleNavigation(role).map((item) => item.label)).toContain('Sites')
    }
  })

  it('offers Add site only to roles that can create sites', () => {
    expect(canCreateSites('owner')).toBe(true)
    expect(canCreateSites('admin')).toBe(true)
    expect(canCreateSites('editor')).toBe(false)
    expect(canCreateSites('viewer')).toBe(false)
  })

  it('hides site-scoped navigation when the organization has no sites', () => {
    expect(visibleNavigation('owner', false).map((item) => item.label)).toEqual([
      'Sites',
      'Data health',
      'Organization settings',
    ])
  })
})
