import { describe, expect, it } from 'vitest'

import type { Membership } from '@/lib/me'

import { buildCrumbs } from './routeCrumbs'

const membership: Membership = {
  org: { id: 1, name: 'Acme', slug: 'acme' },
  role: 'owner',
  sites: [{ id: 7, name: 'Docs', slug: 'docs' }],
}
const empty: Membership = { ...membership, sites: [] }
const labels = (path: string, m = membership) => buildCrumbs(path, m).map((c) => c.label)

describe('buildCrumbs', () => {
  it('dashboard: Org / Site / Dashboard', () => {
    expect(labels('/app/acme/docs')).toEqual(['Acme', 'Docs', 'Dashboard'])
  })
  it('section: Org / Site / Section', () => {
    expect(labels('/app/acme/docs/funnels')).toEqual(['Acme', 'Docs', 'Funnels'])
  })
  it('detail never shows the id', () => {
    expect(labels('/app/acme/docs/funnels/42')).toEqual(['Acme', 'Docs', 'Funnels', 'Detail'])
    expect(labels('/app/acme/docs/profiles/abc-9')).toEqual(['Acme', 'Docs', 'Profiles', 'Detail'])
    expect(labels('/app/acme/docs/links/9')).toEqual(['Acme', 'Docs', 'Links', 'Detail'])
  })
  it('known sub-pages under a section keep their label', () => {
    expect(labels('/app/acme/docs/links/pixel')).toEqual(['Acme', 'Docs', 'Links', 'Pixel'])
  })
  it('site settings', () => {
    expect(labels('/app/acme/docs/settings')).toEqual(['Acme', 'Docs', 'Settings'])
    expect(labels('/app/acme/docs/settings/general')).toEqual(['Acme', 'Docs', 'Settings', 'General'])
  })
  it('org pages', () => {
    expect(labels('/app/acme/health')).toEqual(['Acme', 'Health'])
    expect(labels('/app/acme/sites')).toEqual(['Acme', 'Sites'])
    expect(labels('/app/acme/settings/members')).toEqual(['Acme', 'Settings', 'Members'])
  })
  it('account and admin have no org crumb', () => {
    expect(labels('/app/account/profile')).toEqual(['Account', 'Profile'])
    expect(labels('/app/admin/users')).toEqual(['Admin', 'Users'])
  })
  it('an org with no sites shows no site crumb', () => {
    expect(labels('/app/acme/sites', empty)).toEqual(['Acme', 'Sites'])
  })
  it('an unknown site slug falls back to the slug without throwing', () => {
    expect(labels('/app/acme/ghost/events')).toEqual(['Acme', 'ghost', 'Events'])
  })
  it('an unknown trailing segment is shown raw, never empty', () => {
    expect(labels('/app/acme/docs/zzz')).toEqual(['Acme', 'Docs', 'zzz'])
  })
  it('links every crumb except the last', () => {
    const crumbs = buildCrumbs('/app/acme/docs/funnels/42', membership)
    expect(crumbs.slice(0, -1).every((c) => c.to)).toBe(true)
    expect(crumbs.at(-1)?.to).toBeUndefined()
    expect(crumbs[0]).toMatchObject({ to: '/app/$org/sites', params: { org: 'acme' } })
    expect(crumbs[1]).toMatchObject({ to: '/app/$org/$site', params: { org: 'acme', site: 'docs' } })
  })
  it('ignores a trailing slash', () => {
    expect(labels('/app/acme/docs/funnels/')).toEqual(['Acme', 'Docs', 'Funnels'])
  })
})
