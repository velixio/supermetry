import '@testing-library/jest-dom/vitest'

import { cleanup, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))
vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params, search }: { children: React.ReactNode; to: string; params: Record<string, string>; search?: Record<string, string> }) => (
    <a href={Object.entries(params).reduce((href, [key, value]) => href.replace('$' + key, value), to) + (search ? `?${new URLSearchParams(search)}` : '')}>{children}</a>
  ),
}))

import { ReleaseCompare } from './ReleaseCompare'
import { ReleaseDetail } from './ReleaseDetail'
import { renderWithQuery } from './testing'

const health = (over = {}) => ({
  release: '1.1.0', first_seen: '2026-11-10T00:00:00Z', last_seen: '2026-11-14T00:00:00Z', installs: 10, crash_free_users: 0.97, crash_free_sessions: 0.95, sessions: 2500, users: 700,
  abnormal_sessions: 5, errored_sessions: 40, adoption: 0.6, handled_errors: 1, unhandled_errors: 2, health_source: 'sessions', new_issues: 1, regressed_issues: 0, ...over,
})
const detail = (over = {}) => ({
  release: '1.1.0', first_seen: '2026-11-10T00:00:00Z', last_seen: '2026-11-14T00:00:00Z', created: '2026-11-09T08:00:00Z', status: 'open', ref: 'abc1234', health: health(),
  rollout: [{ day: '2026-11-13', sessions: 60, adoption: 0.6, crash_free_sessions: 0.93, crash_free_users: 0.95 }],
  deploys: [{ id: 1, release: '1.1.0', environment: 'production', name: 'rollout 1', url: '', at: '2026-11-13T10:00:00Z' }],
  new_issues: [{ id: 5, title: 'Null cart', events: 12 }], regressed_issues: [], commit_count: 3, health_source: 'sessions', ...over,
})
const respond = (body: unknown) => privateRequest.mockImplementation((_method: string, path: string) => Promise.resolve(path.includes('/app/releases?') || path.includes('/app/releases') && !path.includes('/releases/') ? { releases: [] } : body))
const detailPath = () => privateRequest.mock.calls.map((call) => call[1] as string).find((path) => /\/releases\/[^?]/.test(path)) ?? ''
const props = { org: 'acme', site: 'desk', siteId: 7, timezone: 'UTC', search: { period: '30d' as const }, onSearch: vi.fn() }

describe('ReleaseDetail', () => {
  beforeEach(() => vi.clearAllMocks())
  afterEach(cleanup)

  it('shows the rollout chart, deploys, issue lists and commit count', async () => {
    respond(detail())
    renderWithQuery(<ReleaseDetail {...props} release="1.1.0" />)
    expect(await screen.findByRole('img', { name: 'Adoption and crash-free sessions per day' })).toBeInTheDocument()
    const deploys = screen.getByRole('list', { name: 'Deploys' })
    expect(deploys).toHaveTextContent('2026-11-13')
    expect(deploys).toHaveTextContent('production')
    expect(screen.getByRole('link', { name: 'Null cart' })).toHaveAttribute('href', '/app/acme/desk/issues/5')
    expect(screen.getByText('No regressions.')).toBeInTheDocument()
    expect(screen.getByText('3 commits')).toBeInTheDocument()
    expect(screen.getByLabelText('Release figures')).toHaveTextContent('60.0%')
    expect(screen.queryByText('from error events')).not.toBeInTheDocument()
    expect(detailPath()).toContain('/api/private/sites/7/app/releases/1.1.0?')
  })

  it('notes rates computed from error events and a release without an API row', async () => {
    respond(detail({ created: null, commit_count: 0, deploys: [], health_source: 'events' }))
    renderWithQuery(<ReleaseDetail {...props} release="1.1.0" />)
    expect((await screen.findAllByText('from error events')).length).toBeGreaterThan(0)
    expect(screen.getByText('Seen in events only')).toBeInTheDocument()
    expect(screen.getByText('No commits')).toBeInTheDocument()
    expect(screen.queryByRole('list', { name: 'Deploys' })).not.toBeInTheDocument()
  })

  it('shows the API error for an unknown release', async () => {
    privateRequest.mockImplementation((_method: string, path: string) => (path.includes('/releases/9.9.9') ? Promise.reject(new Error('not found')) : Promise.resolve({ releases: [] })))
    renderWithQuery(<ReleaseDetail {...props} release="9.9.9" />)
    expect(await screen.findByRole('alert')).toHaveTextContent('not found')
  })
})

describe('ReleaseCompare', () => {
  beforeEach(() => vi.clearAllMocks())
  afterEach(cleanup)

  it('shows both releases and the signed delta', async () => {
    respond({
      a: detail({ release: '1.0.0', health: health({ crash_free_sessions: 0.98 }) }),
      b: detail({ release: '1.1.0', health: health({ crash_free_sessions: 0.95 }) }),
      delta: { crash_free_sessions: -0.03, crash_free_users: null, new_issues: 2 },
      only_in_b: [{ id: 9, title: 'Only here', events: 4 }],
    })
    renderWithQuery(<ReleaseCompare {...props} a="1.0.0" b="1.1.0" />)
    expect(await screen.findByLabelText('Release 1.0.0')).toHaveTextContent('98.0%')
    expect(screen.getByLabelText('Release 1.1.0')).toHaveTextContent('95.0%')
    const delta = screen.getByLabelText('Delta')
    expect(delta).toHaveTextContent('−3.0 pp')
    expect(delta).toHaveTextContent('n/a')
    expect(delta).toHaveTextContent('+2')
    expect(screen.getByRole('link', { name: 'Only here' })).toBeInTheDocument()
    expect(detailPath()).toContain('/releases/compare?')
    expect(detailPath()).toContain('a=1.0.0')
  })

  it('asks for two releases when none are picked', () => {
    renderWithQuery(<ReleaseCompare {...props} a="" b="" />)
    expect(screen.getByText(/Pick two releases/)).toBeInTheDocument()
    expect(detailPath()).toBe('')
  })
})
