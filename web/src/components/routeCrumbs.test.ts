import { describe, expect, it } from 'vitest'

import type { Membership } from '@/lib/me'

import { buildCrumbs } from './routeCrumbs'

const membership: Membership = {
  org: { id: 1, name: 'Acme', slug: 'acme' },
  role: 'owner',
  sites: [{ id: 7, name: 'Docs', slug: 'docs', public_id: 'docs-public' }],
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
  it('finds the site by public id or numeric id, keeping the alias from the URL', () => {
    expect(labels('/app/acme/docs-public/events')).toEqual(['Acme', 'Docs', 'Events'])
    expect(labels('/app/acme/7/events')).toEqual(['Acme', 'Docs', 'Events'])
    expect(buildCrumbs('/app/acme/docs-public/events', membership)[1]).toMatchObject({ params: { org: 'acme', site: 'docs-public' } })
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
  it('links tail crumbs to routes that render', () => {
    const site = { org: 'acme', site: 'docs' }
    expect(buildCrumbs('/app/acme/docs/settings/general', membership)[2]).toEqual({
      label: 'Settings', to: '/app/$org/$site/settings/general', params: site,
    })
    expect(buildCrumbs('/app/acme/docs/funnels/42', membership)[2]).toEqual({
      label: 'Funnels', to: '/app/$org/$site/funnels', params: site,
    })
    expect(buildCrumbs('/app/acme/settings/members', membership)[1]).toEqual({
      label: 'Settings', to: '/app/$org/settings', params: { org: 'acme' },
    })
    expect(buildCrumbs('/app/account/profile', membership)[0]).toEqual({ label: 'Account', to: '/app/account' })
    expect(buildCrumbs('/app/admin/users', membership)[0]).toEqual({ label: 'Admin', to: '/app/admin' })
  })
  it('the final Detail crumb carries no link', () => {
    expect(buildCrumbs('/app/acme/docs/funnels/42', membership).at(-1)).toEqual({ label: 'Detail' })
  })
  it('ignores a trailing slash', () => {
    expect(labels('/app/acme/docs/funnels/')).toEqual(['Acme', 'Docs', 'Funnels'])
  })
})
