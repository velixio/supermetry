import { renderAuthorized as render } from '@/test/authorization'
import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, screen, within } from '@testing-library/react'
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
  has_replay: true,
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

    const table = screen.getByRole('table', { name: 'Sessions' })
    expect(within(table).getByRole('cell', { name: 'ada-42' })).toBeVisible()
    expect(within(table).getByRole('cell', { name: '/ → /pricing' })).toBeVisible()
    expect(within(table).getByRole('columnheader', { name: 'Browser' })).toBeVisible()
    expect(within(table).getByRole('columnheader', { name: 'OS' })).toBeVisible()
    expect(within(table).getByRole('columnheader', { name: 'Device' })).toBeVisible()
    expect(within(table).getByText('San Francisco, United States')).toBeVisible()
    expect(within(table).queryByText('US')).not.toBeInTheDocument()
    expect(within(table).getByText('Chrome')).toBeVisible()
    expect(within(table).getByText('Linux')).toBeVisible()
    expect(within(table).getByText('Desktop')).toBeVisible()
    fireEvent.click(within(table).getByRole('button', { name: 'View session 301' }))
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

  it('renders every session dimension in an accessible mobile card', () => {
    const onSelect = vi.fn()
    const onReplayChange = vi.fn()
    useSessions.mockReturnValue({ data: { data: [summary] }, isPending: false, isError: false, isFetching: false })
    useSession.mockReturnValue({ isPending: false, isError: false })

    render(<SessionsPage cursor={undefined} dateRange={{ from: '2026-10-01', to: '2026-10-08' }} filters={[]} page={1} onFirst={() => undefined} onNext={() => undefined} onPrevious={() => undefined} onSelect={onSelect} selectedSessionId={undefined} siteId={7} org="acme" site="example" onReplayChange={onReplayChange} />)

    const cards = screen.getByRole('list', { name: 'Sessions' })
    expect(cards).toHaveClass('md:hidden')
    const card = within(cards).getByRole('listitem', { name: /session started/i })
    expect(within(card).getByText('/ → /pricing')).toBeVisible()
    expect(within(card).getByText('San Francisco, United States')).toBeVisible()
    expect(within(card).getByText('Google')).toBeVisible()
    expect(within(card).getByText('Chrome')).toBeVisible()
    expect(within(card).getByText('Linux')).toBeVisible()
    expect(within(card).getByText('Desktop')).toBeVisible()
    expect(within(card).getByText('ada-42')).toBeVisible()
    expect(within(card).getByText('2m 0s')).toBeVisible()
    expect(within(card).getByText('2 pages')).toBeVisible()
    expect(within(card).getByText('1 event')).toBeVisible()

    const timelineButton = within(card).getByRole('button', { name: 'View session 301' })
    const replayButton = within(card).getByRole('button', { name: 'Play replay' })
    expect(timelineButton).toHaveClass('min-h-11')
    expect(replayButton).toHaveClass('size-11')
    fireEvent.click(timelineButton)
    fireEvent.click(replayButton)
    expect(onSelect).toHaveBeenCalledWith('301')
    expect(onReplayChange).toHaveBeenCalledWith('301')
  })

  it('keeps the first table column sticky and defers lower-priority columns', () => {
    useSessions.mockReturnValue({ data: { data: [summary] }, isPending: false, isError: false, isFetching: false })
    useSession.mockReturnValue({ isPending: false, isError: false })

    render(<SessionsPage cursor={undefined} dateRange={{ from: '2026-10-01', to: '2026-10-08' }} filters={[]} page={1} onFirst={() => undefined} onNext={() => undefined} onPrevious={() => undefined} onSelect={() => undefined} selectedSessionId={undefined} siteId={7} />)

    expect(screen.getByRole('table', { name: 'Sessions' })).toHaveClass('min-w-[680px]')
    expect(screen.getByRole('columnheader', { name: 'Started' })).toHaveClass('sticky', 'left-0')
    expect(screen.getByRole('columnheader', { name: 'Browser' })).toHaveClass('hidden', 'lg:table-cell')
    expect(screen.getByRole('columnheader', { name: 'Location' })).toHaveClass('hidden', 'lg:table-cell')
    expect(screen.getByRole('columnheader', { name: 'Source' })).toHaveClass('hidden', 'xl:table-cell')
    expect(screen.getByRole('columnheader', { name: 'Profile' })).toHaveClass('hidden', 'xl:table-cell')
  })
})
