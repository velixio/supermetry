import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { useSession, useSessions } = vi.hoisted(() => ({
  useSession: vi.fn(),
  useSessions: vi.fn(),
}))
vi.mock('./api', () => ({ useSession, useSessions }))

import { SessionsPage } from './SessionsPage'

afterEach(cleanup)

const summary = {
  id: '301',
  started_at: '2026-10-08T08:00:00Z',
  duration_ms: 120000,
  pageviews: 2,
  events: 1,
  entry_page: '/',
  exit_page: '/pricing',
  source: 'Google',
  country: 'US',
  city: 'San Francisco',
  device: 'desktop',
  browser: 'Chrome',
  os: 'Linux',
  external_id: 'ada-42',
}

describe('SessionsPage', () => {
  it('renders full visitor dimensions and selects a timeline', () => {
    const onSelect = vi.fn()
    useSessions.mockReturnValue({
      data: { data: [summary], next_cursor: 'next-page' },
      isPending: false,
      isError: false,
      isFetching: false,
    })
    useSession.mockReturnValue({ isPending: false, isError: false })

    render(<SessionsPage cursor={undefined} dateRange={{ from: '2026-10-01', to: '2026-10-08' }} filters={[]} page={1} onFirst={() => undefined} onNext={() => undefined} onPrevious={() => undefined} onSelect={onSelect} selectedSessionId={undefined} siteId={7} />)

    expect(screen.getByRole('cell', { name: 'ada-42' })).toBeVisible()
    expect(screen.getByRole('cell', { name: '/ → /pricing' })).toBeVisible()
    expect(screen.getByRole('columnheader', { name: 'Browser' })).toBeVisible()
    expect(screen.getByRole('columnheader', { name: 'OS' })).toBeVisible()
    expect(screen.getByRole('columnheader', { name: 'Device' })).toBeVisible()
    expect(screen.getByText('San Francisco, United States')).toBeVisible()
    expect(screen.queryByText('US')).not.toBeInTheDocument()
    expect(screen.getByText('Chrome')).toBeVisible()
    expect(screen.getByText('Linux')).toBeVisible()
    expect(screen.getByText('Desktop')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'View session 301' }))
    expect(onSelect).toHaveBeenCalledWith('301')
  })

  it('shows the selected session timeline in an accessible drawer', () => {
    useSessions.mockReturnValue({ data: { data: [summary] }, isPending: false, isError: false, isFetching: false })
    useSession.mockReturnValue({
      data: {
        session: summary,
        timeline: [
          { ts: '2026-10-08T08:00:00Z', kind: 'pageview', name: 'pageview', page: '/', props: null },
          { ts: '2026-10-08T08:01:00Z', kind: 'event', name: 'signup', page: '/pricing', props: { plan: 'pro' } },
        ],
      },
      isPending: false,
      isError: false,
    })

    render(<SessionsPage cursor={undefined} dateRange={{ from: '2026-10-01', to: '2026-10-08' }} filters={[]} page={1} onFirst={() => undefined} onNext={() => undefined} onPrevious={() => undefined} onSelect={() => undefined} selectedSessionId="301" siteId={7} />)

    expect(screen.getByRole('dialog', { name: 'Session timeline' })).toBeVisible()
    expect(screen.getByText('signup')).toBeVisible()
    expect(screen.getByText('plan: pro')).toBeVisible()
    expect(screen.getByText(/anonymous session IDs reset daily/i)).toBeVisible()
  })
})
