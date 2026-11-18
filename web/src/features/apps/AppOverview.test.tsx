import '@testing-library/jest-dom/vitest'

import { cleanup, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))
vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params }: { children: React.ReactNode; to: string; params: Record<string, string> }) => <a href={Object.entries(params).reduce((href, [key, value]) => href.replace('$' + key, value), to)}>{children}</a>,
}))

import { AppOverview } from './AppOverview'
import { issueRow, renderWithQuery } from './testing'

const overview = (over = {}) => ({
  events: 1200, crashes: 30, new_issues: 4, regressed_issues: 2, crash_free_users: 0.987, crash_free_sessions: 0.95,
  sessions: 200, users: 80, abnormal_sessions: 4, errored_sessions: 16, handled_errors: 30, unhandled_errors: 12, health_source: 'sessions',
  series: [{ day: '2026-11-14', events: 600, crashes: 10, crash_free_users: 0.99 }], ...over,
})

function mock(data: ReturnType<typeof overview>) {
  privateRequest.mockImplementation((_method: string, path: string) => {
    if (path.includes('/overview')) return Promise.resolve(data)
    if (path.includes('/platforms')) return Promise.resolve({ platforms: [{ platform: 'android', events: 100, crashes: 10, crash_free_users: 0.9, crash_free_sessions: null, symbolicated: 40, missing: 5 }, { platform: 'ios', events: 50, crashes: 0, crash_free_users: 1, crash_free_sessions: 1, symbolicated: 0, missing: 0 }] })
    if (path.includes('/issues')) return Promise.resolve({ issues: [issueRow()], total: 1, next_offset: null })
    return Promise.resolve({ releases: [] })
  })
}
const props = { org: 'acme', site: 'desk', siteId: 7, timezone: 'UTC', search: { period: '30d' as const }, onSearch: vi.fn() }

describe('AppOverview', () => {
  beforeEach(() => vi.clearAllMocks())
  afterEach(cleanup)

  it('renders the KPI tiles and the top issues', async () => {
    mock(overview())
    renderWithQuery(<AppOverview {...props} />)
    expect(await screen.findByText('98.7%')).toBeInTheDocument()
    expect(screen.getByText('95.0%')).toBeInTheDocument()
    expect(screen.getByText('1,200')).toBeInTheDocument()
    expect(within(screen.getByLabelText('Key figures')).getByText('Crashes').nextSibling).toHaveTextContent('30')
    expect(screen.getByText('New issues').nextSibling).toHaveTextContent('4')
    expect(screen.getByText('Regressed issues').nextSibling).toHaveTextContent('2')
    expect(await screen.findByRole('link', { name: 'Segfault in renderer' })).toHaveAttribute('href', '/app/acme/desk/issues/11')
  })

  it('breaks health and symbolication down per platform', async () => {
    mock(overview())
    renderWithQuery(<AppOverview {...props} />)
    const table = await screen.findByRole('table', { name: 'Platforms' })
    const rows = within(table).getAllByRole('row')
    expect(rows[1]).toHaveTextContent('android')
    expect(rows[1]).toHaveTextContent('90.0%')
    expect(rows[1]).toHaveTextContent('n/a')
    expect(rows[2]).toHaveTextContent('ios')
  })

  it('shows n/a with a hint when crash-free sessions are null', async () => {
    mock(overview({ crash_free_sessions: null }))
    renderWithQuery(<AppOverview {...props} />)
    const na = await within(await screen.findByLabelText('Key figures')).findByText('n/a')
    expect(na).toHaveAttribute('title', 'Send a session event at app start to measure this')
  })

  it('shows the empty state when there are no events and no filters', async () => {
    mock(overview({ events: 0, crashes: 0, series: [] }))
    renderWithQuery(<AppOverview {...props} />)
    expect(await screen.findByText('No reports yet')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Set up this app' })).toBeInTheDocument()
  })

  it('shows session status and handled split, and the events note on v1 data', async () => {
    mock(overview())
    const view = renderWithQuery(<AppOverview {...props} />)
    const status = await screen.findByLabelText('Session status breakdown')
    expect(status).toHaveTextContent('Healthy170')
    expect(status).toHaveTextContent('Errored16')
    expect(status).toHaveTextContent('Abnormal4')
    expect(status).toHaveTextContent('Crashed10')
    expect(screen.getByRole('img', { name: 'Handled errors 30, unhandled errors 12' })).toBeInTheDocument()
    expect(screen.queryByText(/from error events/)).toBeNull()
    view.unmount()
    mock(overview({ sessions: 0, health_source: 'events' }))
    renderWithQuery(<AppOverview {...props} search={{ period: '30d' as const, release: '1.0.0' }} />)
    expect(await screen.findByText(/from error events/)).toBeInTheDocument()
  })
})
