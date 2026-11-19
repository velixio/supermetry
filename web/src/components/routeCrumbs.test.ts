import { describe, expect, it } from 'vitest'

import type { Membership } from '@/lib/me'

import { buildCrumbs, isOrgScopedPath } from './routeCrumbs'

const membership: Membership = {
  org: { id: 1, name: 'Acme', slug: 'acme' },
  role: 'owner',
  sites: [{ id: 7, name: 'Docs', slug: 'docs', public_id: 'docs-public' }],
}
const appMembership: Membership = { ...membership, sites: [{ id: 8, name: 'Desk', slug: 'desk', public_id: 'desk-public', kind: 'app' }, { id: 9, name: 'App', slug: 'app', public_id: 'app-public', kind: 'web' }] }
const empty: Membership = { ...membership, sites: [] }
const labels = (path: string, m = membership) => buildCrumbs(path, m).map((c) => c.label)

describe('buildCrumbs', () => {
  it('app pages use capitalised labels and call the landing page Overview', () => {
    expect(labels('/app/acme/desk', appMembership)).toEqual(['Overview'])
    expect(labels('/app/acme/desk/issues', appMembership)).toEqual(['Issues'])
    expect(labels('/app/acme/desk/issues/4', appMembership)).toEqual(['Issues', 'Detail'])
    expect(labels('/app/acme/desk/compatibility', appMembership)).toEqual(['Compatibility'])
    expect(labels('/app/acme/desk/releases', appMembership)).toEqual(['Releases'])
    expect(labels('/app/acme/desk/settings/app', appMembership)).toEqual(['Settings', 'App'])
  })
  it('a site that happens to be called app is still a site', () => {
    expect(labels('/app/acme/app', appMembership)).toEqual(['Dashboard'])
  })
  it('dashboard: just Dashboard', () => {
    expect(labels('/app/acme/docs')).toEqual(['Dashboard'])
  })
  it('section: just the section', () => {
    expect(labels('/app/acme/docs/funnels')).toEqual(['Funnels'])
  })
  it('detail never shows the id', () => {
    expect(labels('/app/acme/docs/funnels/42')).toEqual(['Funnels', 'Detail'])
    expect(labels('/app/acme/docs/profiles/abc-9')).toEqual(['Profiles', 'Detail'])
    expect(labels('/app/acme/docs/links/9')).toEqual(['Links', 'Detail'])
  })
  it('known sub-pages under a section keep their label', () => {
    expect(labels('/app/acme/docs/links/pixel')).toEqual(['Links', 'Pixel'])
  })
  it('site settings', () => {
    expect(labels('/app/acme/docs/settings')).toEqual(['Settings'])
    expect(labels('/app/acme/docs/settings/general')).toEqual(['Settings', 'General'])
  })
  it('org pages', () => {
    expect(labels('/app/acme/health')).toEqual(['Health'])
    expect(labels('/app/acme/sites')).toEqual(['Projects'])
    expect(labels('/app/acme/settings/members')).toEqual(['Settings', 'Members'])
  })
  it('account and admin have no org crumb', () => {
    expect(labels('/app/account/profile')).toEqual(['Account', 'Profile'])
    expect(labels('/app/admin/users')).toEqual(['Admin', 'Users'])
    expect(labels('/app/admin/update')).toEqual(['Admin', 'System update'])
  })
  it('keeps the alias from the URL', () => {
    expect(labels('/app/acme/docs-public/events')).toEqual(['Events'])
    expect(labels('/app/acme/7/events')).toEqual(['Events'])
    expect(buildCrumbs('/app/acme/docs-public/funnels/4', membership)[0]).toMatchObject({ params: { org: 'acme', site: 'docs-public' } })
  })
  it('an org with no sites shows only the page', () => {
    expect(labels('/app/acme/sites', empty)).toEqual(['Projects'])
  })
  it('an unknown site slug does not throw', () => {
    expect(labels('/app/acme/ghost/events')).toEqual(['Events'])
  })
  it('an unknown trailing segment is shown raw, never empty', () => {
    expect(labels('/app/acme/docs/zzz')).toEqual(['zzz'])
  })
  it('links every crumb except the last', () => {
    const crumbs = buildCrumbs('/app/acme/docs/funnels/42', membership)
    expect(crumbs.slice(0, -1).every((c) => c.to)).toBe(true)
    expect(crumbs.at(-1)?.to).toBeUndefined()
    expect(crumbs[0]).toMatchObject({ label: 'Funnels', to: '/app/$org/$site/funnels', params: { org: 'acme', site: 'docs' } })
  })
  it('links tail crumbs to routes that render', () => {
    const site = { org: 'acme', site: 'docs' }
    expect(buildCrumbs('/app/acme/docs/settings/general', membership)[0]).toEqual({
      label: 'Settings', to: '/app/$org/$site/settings/general', params: site,
    })
    expect(buildCrumbs('/app/acme/docs/funnels/42', membership)[0]).toEqual({
      label: 'Funnels', to: '/app/$org/$site/funnels', params: site,
    })
    expect(buildCrumbs('/app/acme/settings/members', membership)[0]).toEqual({
      label: 'Settings', to: '/app/$org/settings', params: { org: 'acme' },
    })
    expect(buildCrumbs('/app/account/profile', membership)[0]).toEqual({ label: 'Account', to: '/app/account' })
    expect(buildCrumbs('/app/admin/users', membership)[0]).toEqual({ label: 'Admin', to: '/app/admin' })
  })
  it('the final Detail crumb carries no link', () => {
    expect(buildCrumbs('/app/acme/docs/funnels/42', membership).at(-1)).toEqual({ label: 'Detail' })
  })
  it('ignores a trailing slash', () => {
    expect(labels('/app/acme/docs/funnels/')).toEqual(['Funnels'])
  })
  it('without a membership keeps account and admin crumbs and shows none for an organization path', () => {
    expect(buildCrumbs('/app/account/security').map((c) => c.label)).toEqual(['Account', 'Security'])
    expect(buildCrumbs('/app/admin/sites').map((c) => c.label)).toEqual(['Admin', 'Projects'])
    expect(buildCrumbs('/app/missing')).toEqual([])
    expect(buildCrumbs('/app/missing/docs/events')).toEqual([])
    expect(buildCrumbs('/app')).toEqual([])
  })
  it('classifies org-scoped pages for the switcher', () => {
    for (const path of ['/app/acme/settings/members', '/app/acme/sites', '/app/acme/health', '/app/acme/github-stars', '/app/account/profile', '/app/admin/users']) expect(isOrgScopedPath(path)).toBe(true)
    for (const path of ['/app/acme/docs', '/app/acme/docs/funnels/4', '/app/acme/docs/settings/general']) expect(isOrgScopedPath(path)).toBe(false)
  })
})
