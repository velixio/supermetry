import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { Membership } from '@/lib/me'

import { CARRIES_SEARCH, pagePath, parsePage, RELATED, relatedLinks } from './relatedConfig'
import { readRecents, recentsFor, recordRecent } from './recents'
import { recommend } from './recommend'
import { DEFAULT_OPEN_MIN_WIDTH, readCollapsedSections, readPanelOpen, writeCollapsedSections, writePanelOpen } from './storage'
import { pinPageKey } from './ContextPanel'

const membership: Membership = { org: { id: 1, name: 'Acme', slug: 'acme' }, role: 'owner', sites: [{ id: 7, name: 'Docs', slug: 'docs' }] }

describe('parsePage', () => {
  it('names site, organization and account pages', () => {
    expect(parsePage('/app/acme/docs')).toEqual({ org: 'acme', site: 'docs', key: 'dashboard' })
    expect(parsePage('/app/acme/docs/sessions')).toEqual({ org: 'acme', site: 'docs', key: 'sessions' })
    expect(parsePage('/app/acme/docs/replays/abc')).toEqual({ org: 'acme', site: 'docs', key: 'replays' })
    expect(parsePage('/app/acme/docs/settings/replay')).toEqual({ org: 'acme', site: 'docs', key: 'settings/replay' })
    expect(parsePage('/app/acme/settings/members')).toEqual({ org: 'acme', key: 'org/members' })
    expect(parsePage('/app/acme/settings')).toEqual({ org: 'acme', key: 'org/overview' })
    expect(parsePage('/app/acme/sites')).toEqual({ org: 'acme', key: 'sites' })
    expect(parsePage('/app/account/security')).toEqual({ key: 'account/security' })
    expect(parsePage('/login')).toEqual({})
  })
})

describe('related pages', () => {
  it('maps every page to 2 to 5 distinct targets that exist as keys of their own kind', () => {
    for (const [key, targets] of Object.entries(RELATED)) {
      expect(targets.length, key).toBeGreaterThanOrEqual(2)
      expect(targets.length, key).toBeLessThanOrEqual(5)
      expect(new Set(targets).size, key).toBe(targets.length)
      expect(targets, key).not.toContain(key)
    }
  })

  it('follows the documented flows', () => {
    expect(RELATED.sessions).toEqual(['replays', 'events', 'journeys'])
    expect(RELATED.events).toEqual(['sessions', 'funnels', 'goals'])
    expect(RELATED.retention).toEqual(['cohorts', 'profiles', 'journeys'])
    expect(RELATED.funnels).toEqual(['goals', 'journeys', 'replays'])
    expect(RELATED.revenue).toEqual(['goals', 'dashboard', 'funnels'])
  })

  it('carries period, comparison and filters only between pages that read them', () => {
    const search = { period: '7d', compare: 'previous_period', f: [], metric: 'visitors' }
    const links = relatedLinks(parsePage('/app/acme/docs/sessions'), search)
    expect(links.map((link) => link.href)).toEqual([
      '/app/acme/docs/replays?period=7d&compare=previous_period',
      '/app/acme/docs/events?period=7d&compare=previous_period',
      '/app/acme/docs/journeys?period=7d&compare=previous_period',
    ])
    // Funnels and goals have no search params: plain links.
    expect(CARRIES_SEARCH.has('funnels')).toBe(false)
    expect(relatedLinks(parsePage('/app/acme/docs/events'), search).map((link) => link.href)).toEqual([
      '/app/acme/docs/sessions?period=7d&compare=previous_period', '/app/acme/docs/funnels', '/app/acme/docs/goals',
    ])
  })

  it('does not carry search from a page that ignores it, and drops project pages without a project', () => {
    expect(relatedLinks(parsePage('/app/acme/docs/settings/goals'), { period: '7d' }).map((link) => link.href)[0]).toBe('/app/acme/docs/goals')
    expect(relatedLinks({ org: 'acme', key: 'sites' }).map((link) => link.key)).toEqual(['health', 'github-stars'])
    expect(relatedLinks({ org: 'acme', key: 'dashboard' })).toEqual([])
    expect(relatedLinks({})).toEqual([])
  })

  it('builds paths for every key kind', () => {
    expect(pagePath('org/overview', 'acme')).toBe('/app/acme/settings')
    expect(pagePath('org/api-keys', 'acme')).toBe('/app/acme/settings/api-keys')
    expect(pagePath('account/profile')).toBe('/app/account/profile')
    expect(pagePath('dashboard', 'acme', 'docs')).toBe('/app/acme/docs')
    expect(pagePath('events', 'acme')).toBeUndefined()
  })
})

describe('pin keys', () => {
  it('turn sidebar pins into page keys', () => {
    expect(pinPageKey('nav:/')).toBe('dashboard')
    expect(pinPageKey('nav:/events')).toBe('events')
    expect(pinPageKey('settings:site:replay')).toBe('settings/replay')
    expect(pinPageKey('settings:org:members')).toBe('org/members')
    expect(pinPageKey('other')).toBeUndefined()
  })
})

describe('recents', () => {
  beforeEach(() => localStorage.clear())

  it('records visits newest first and dedupes by path', () => {
    recordRecent(1, '/app/acme/docs/sessions', '?period=7d', membership)
    recordRecent(1, '/app/acme/docs/events', '', membership)
    const list = recordRecent(1, '/app/acme/docs/sessions', '?period=30d', membership)
    expect(list.map((item) => item.href)).toEqual(['/app/acme/docs/sessions?period=30d', '/app/acme/docs/events'])
    expect(readRecents(1)).toEqual(list)
    expect(readRecents(2)).toEqual([])
  })

  it('labels pages from their breadcrumbs and skips pages without any', () => {
    recordRecent(1, '/app/acme/docs/settings/general', '', membership)
    expect(readRecents(1)[0].label).toBe('Settings / General')
    recordRecent(1, '/login', '', membership)
    expect(readRecents(1)).toHaveLength(1)
  })

  it('shows at most 8 of this organization and project, without the current page', () => {
    for (let i = 0; i < 12; i += 1) recordRecent(1, `/app/acme/docs/replays/r${i}`, '', membership)
    recordRecent(1, '/app/acme/other/events', '', { ...membership, sites: [...membership.sites, { id: 8, name: 'Other', slug: 'other' }] })
    recordRecent(1, '/app/acme/health', '', membership)
    const shown = recentsFor(readRecents(1), 'acme', 'docs', '/app/acme/docs/replays/r11')
    expect(shown).toHaveLength(8)
    expect(shown[0].path).toBe('/app/acme/health')
    expect(shown.some((item) => item.path.includes('other'))).toBe(false)
    expect(shown.some((item) => item.path.endsWith('/r11'))).toBe(false)
  })

  it('survives blocked storage', () => {
    const spy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked') })
    expect(() => recordRecent(1, '/app/acme/docs/events', '', membership)).not.toThrow()
    spy.mockRestore()
  })
})

describe('recommendations', () => {
  const where = { org: 'acme', site: 'docs', pathname: '/app/acme/docs' }

  it('derives actions only from known facts, at most three, most urgent first', () => {
    expect(recommend({}, where)).toEqual([])
    expect(recommend({ installStatus: 'waiting', rejectedEvents: 4, replayOff: true, goalCount: 0 }, where).map((item) => item.id)).toEqual(['install', 'rejected', 'recordingOff'])
    expect(recommend({ installStatus: 'ok', goalCount: 0 }, where).map((item) => item.id)).toEqual(['createGoal'])
    expect(recommend({ installStatus: 'ok', goalCount: 2, replayOff: false, rejectedEvents: 0 }, where)).toEqual([])
  })

  it('skips the page the user is on', () => {
    expect(recommend({ replayOff: true }, { ...where, pathname: '/app/acme/docs/settings/replay' })).toEqual([])
    expect(recommend({ replayOff: true }, where)[0].href).toBe('/app/acme/docs/settings/replay')
  })
})

describe('storage', () => {
  beforeEach(() => localStorage.clear())

  it('defaults open from 1536px and remembers the choice per user', () => {
    expect(readPanelOpen(1, DEFAULT_OPEN_MIN_WIDTH)).toBe(true)
    expect(readPanelOpen(1, DEFAULT_OPEN_MIN_WIDTH - 1)).toBe(false)
    writePanelOpen(1, false)
    expect(readPanelOpen(1, 2000)).toBe(false)
    expect(readPanelOpen(2, 2000)).toBe(true)
    writeCollapsedSections(1, ['recent'])
    expect(readCollapsedSections(1)).toEqual(['recent'])
    expect(readCollapsedSections(2)).toEqual([])
  })

  it('falls back when storage throws', () => {
    const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked') })
    expect(readPanelOpen(1, 2000)).toBe(true)
    expect(readCollapsedSections(1)).toEqual([])
    spy.mockRestore()
  })
})
