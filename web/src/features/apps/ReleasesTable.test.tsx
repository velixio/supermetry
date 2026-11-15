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

import { ReleasesTable } from './ReleasesTable'
import { renderWithQuery } from './testing'

const releases = [
  { release: '1.2.0', first_seen: '2026-11-10T00:00:00Z', last_seen: '2026-11-14T00:00:00Z', installs: 1200, crash_free_users: 0.991, crash_free_sessions: null, new_issues: 3, regressed_issues: 0 },
  { release: '1.1.0', first_seen: '2026-10-01T00:00:00Z', last_seen: '2026-11-01T00:00:00Z', installs: 800, crash_free_users: 0.97, crash_free_sessions: 0.96, new_issues: 0, regressed_issues: 2 },
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
})
