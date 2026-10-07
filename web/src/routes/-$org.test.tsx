import { describe, expect, it } from 'vitest'

import {
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
})
