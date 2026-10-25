import { describe, expect, it } from 'vitest'

import type { Membership, Site } from './me'
import { orgSwitchHref, portableSearchParams, siteSwitchHref } from './switchTarget'

const identified = { instance_mode: 'identified' as const, configured_profiles_enabled: true, effective_mode: 'identified' as const, restrictions: [] as string[] }
const anonymous = { instance_mode: 'anonymous' as const, configured_profiles_enabled: false, effective_mode: 'anonymous' as const, restrictions: [] as string[] }
const web: Site = { id: 2, name: 'B', slug: 'site-b', kind: 'web', tracking_policy: identified }
const pixel: Site = { id: 3, name: 'P', slug: 'pix', kind: 'pixel', tracking_policy: identified }
const anon: Site = { id: 4, name: 'A', slug: 'anon', kind: 'web', tracking_policy: anonymous }
const owner: Membership = {
  org: { id: 1, name: 'Acme', slug: 'acme' }, role: 'owner', sites: [web, pixel, anon],
  effective_capabilities: ['profiles.read', 'replay.read', 'data.export'],
}
const viewer: Membership = { ...owner, role: 'viewer', effective_capabilities: [] }
const query = (href: string) => Object.fromEntries(new URL(href, 'https://x.test').searchParams)
const path = (href: string) => new URL(href, 'https://x.test').pathname

describe('site switch target', () => {
  it.each([
    ['/app/$org/$site/settings/tracking', '/app/acme/site-b/settings/tracking'],
    ['/app/$org/$site/sessions', '/app/acme/site-b/sessions'],
    ['/app/$org/$site/funnels/$id', '/app/acme/site-b/funnels'],
    ['/app/$org/$site/profiles/$id', '/app/acme/site-b/profiles'],
    ['/app/$org/$site/replays/$id', '/app/acme/site-b/replays'],
    ['/app/$org/$site/links/$id', '/app/acme/site-b/links'],
    ['/app/$org/$site/links/pixel_/$id', '/app/acme/site-b/links/pixel'],
    ['/app/$org/$site/funnels/', '/app/acme/site-b/funnels'],
    ['/app/$org/$site/', '/app/acme/site-b'],
    ['/app/$org/settings/members', '/app/acme/site-b'],
    ['/app/account/profile', '/app/acme/site-b'],
  ])('maps %s to %s', (routeId, expected) => {
    expect(path(siteSwitchHref({ routeId, search: {} }, owner, web))).toBe(expected)
  })

  it('keeps site-independent search params and drops site-specific ones', () => {
    const href = siteSwitchHref({
      routeId: '/app/$org/$site/sessions',
      search: { period: '7d', compare: 'previous_period', granularity: 'hour', metric: 'pageviews', include_imported: false, dash: '12', segment: 4, session: 'abc', f: ['country:is:US', 'goal:is:Signup'] },
    }, owner, web)
    const params = query(href)
    expect(params).toMatchObject({ period: '7d', compare: 'previous_period', granularity: 'hour', metric: 'pageviews', include_imported: 'false' })
    expect(params).not.toHaveProperty('dash')
    expect(params).not.toHaveProperty('segment')
    expect(params).not.toHaveProperty('session')
    expect(href).toContain('country')
    expect(href).not.toContain('Signup')
  })

  it('falls back to the dashboard when the new site or member cannot use the page', () => {
    expect(path(siteSwitchHref({ routeId: '/app/$org/$site/sessions', search: {} }, owner, pixel))).toBe('/app/acme/pix')
    expect(path(siteSwitchHref({ routeId: '/app/$org/$site/settings/tracking', search: {} }, viewer, web))).toBe('/app/acme/site-b')
    expect(path(siteSwitchHref({ routeId: '/app/$org/$site/profiles/', search: {} }, viewer, web))).toBe('/app/acme/site-b')
    expect(path(siteSwitchHref({ routeId: '/app/$org/$site/replays/$id', search: {} }, owner, anon))).toBe('/app/acme/anon')
    expect(path(siteSwitchHref({ routeId: '/app/$org/$site/journeys', search: {} }, owner, anon))).toBe('/app/acme/anon')
    // The dashboard fallback still carries the period.
    expect(query(siteSwitchHref({ routeId: '/app/$org/$site/sessions', search: { period: '7d' } }, owner, pixel)).period).toBe('7d')
  })

  it('filters portable params without inventing any', () => {
    expect(portableSearchParams({ inspect: 'page', cursors: ['a'] })).toEqual({})
  })
})

describe('organization switch target', () => {
  const other: Membership = { ...owner, org: { id: 2, name: 'Beta', slug: 'beta' }, sites: [web] }
  const empty: Membership = { ...owner, org: { id: 3, name: 'Empty', slug: 'empty' }, sites: [] }
  it('keeps organization-level pages and sends site pages home', () => {
    expect(orgSwitchHref({ routeId: '/app/$org/settings/members', search: {} }, other)).toBe('/app/beta/settings/members')
    expect(orgSwitchHref({ routeId: '/app/$org/health', search: {} }, other)).toBe('/app/beta/health')
    expect(orgSwitchHref({ routeId: '/app/$org/github-stars/$owner/$name', search: {} }, other)).toBe('/app/beta/github-stars')
    expect(path(orgSwitchHref({ routeId: '/app/$org/$site/sessions', search: {} }, other))).toBe('/app/beta/site-b')
    expect(orgSwitchHref({ routeId: '/app/$org/$site/sessions', search: {} }, empty)).toBe('/app/empty/settings')
  })
})
