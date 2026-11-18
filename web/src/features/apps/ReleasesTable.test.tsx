import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))
vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params, search }: { children: React.ReactNode; to: string; params: Record<string, string>; search?: Record<string, string> }) => (
    <a href={Object.entries(params).reduce((href, [key, value]) => href.replace('$' + key, value), to) + (search ? `?${new URLSearchParams(search)}` : '')}>{children}</a>
  ),
}))

import { ReleasesTable } from './ReleasesTable'
import { renderWithQuery } from './testing'

const releases = [
  { release: '1.2.0', first_seen: '2026-11-10T00:00:00Z', last_seen: '2026-11-14T00:00:00Z', installs: 1200, crash_free_users: 0.991, crash_free_sessions: null, sessions: 0, users: 0, abnormal_sessions: 0, errored_sessions: 0, adoption: null, handled_errors: 0, unhandled_errors: 0, health_source: 'events', new_issues: 3, regressed_issues: 0 },
  { release: '1.1.0', first_seen: '2026-10-01T00:00:00Z', last_seen: '2026-11-01T00:00:00Z', installs: 800, crash_free_users: 0.97, crash_free_sessions: 0.96, sessions: 2500, users: 700, abnormal_sessions: 5, errored_sessions: 40, adoption: 0.75, handled_errors: 10, unhandled_errors: 4, health_source: 'sessions', new_issues: 0, regressed_issues: 2 },
]

describe('ReleasesTable', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    privateRequest.mockResolvedValue({ releases })
  })
  afterEach(cleanup)

  it('lists releases in the order given with rates and issue links', async () => {
    renderWithQuery(<ReleasesTable org="acme" site="desk" siteId={7} timezone="UTC" search={{ period: '30d' }} onSearch={vi.fn()} />)
    const first = (await screen.findByText('1.2.0')).closest('tr')!
    expect(first).toHaveTextContent('2026-11-10')
    expect(first).toHaveTextContent('1,200')
    expect(first).toHaveTextContent('99.1%')
    expect(first).toHaveTextContent('n/a')
    const rowsText = screen.getAllByRole('row').map((row) => row.textContent)
    expect(rowsText[1]).toContain('1.2.0')
    expect(rowsText[2]).toContain('1.1.0')
    expect(screen.getByRole('link', { name: '3' })).toHaveAttribute('href', '/app/acme/desk/issues?period=30d&release=1.2.0&status=all')
    expect(screen.getByRole('link', { name: '2' })).toHaveAttribute('href', '/app/acme/desk/issues?period=30d&release=1.1.0&status=regressed')
  })

  it('shows adoption, sessions and a note only for rows computed from error events', async () => {
    renderWithQuery(<ReleasesTable org="acme" site="desk" siteId={7} timezone="UTC" search={{ period: '30d' }} onSearch={vi.fn()} />)
    const sessionsRow = (await screen.findByText('1.1.0')).closest('tr')!
    expect(sessionsRow).toHaveTextContent('75.0%')
    expect(sessionsRow).toHaveTextContent('96.0%')
    expect(sessionsRow).toHaveTextContent('2,500')
    expect(sessionsRow).not.toHaveTextContent('from error events')
    expect(screen.getByText('1.2.0').closest('tr')).toHaveTextContent('from error events')
    const href = screen.getByRole('link', { name: '1.1.0' }).getAttribute('href')!
    expect(href).toBe('/app/acme/desk/releases/1.1.0?period=30d')
  })

  it('compares two ticked releases', async () => {
    renderWithQuery(<ReleasesTable org="acme" site="desk" siteId={7} timezone="UTC" search={{ period: '30d' }} onSearch={vi.fn()} />)
    await screen.findByText('1.2.0')
    expect(screen.queryByRole('link', { name: 'Compare selected' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('checkbox', { name: 'Compare 1.1.0' }))
    fireEvent.click(screen.getByRole('checkbox', { name: 'Compare 1.2.0' }))
    expect(screen.getByRole('link', { name: 'Compare selected' })).toHaveAttribute('href', '/app/acme/desk/releases/compare?period=30d&a=1.1.0&b=1.2.0')
  })
})
