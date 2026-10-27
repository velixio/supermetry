import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ useTrending: vi.fn(), useSetTrendingEnabled: vi.fn() }))
vi.mock('./trendingApi', async (importOriginal) => ({ ...(await importOriginal<typeof import('./trendingApi')>()), ...api }))

import { StarsTrending } from './StarsTrending'

const sighting = (over: Record<string, unknown> = {}) => ({
  id: 1, list_language: '', since: 'daily', first_rank: 9, best_rank: 4, last_rank: 7,
  first_seen_at: '2026-10-27T10:00:00Z', last_seen_at: '2026-10-27T11:00:00Z', left_at: null, ...over,
})

const status = (over: Record<string, unknown> = {}) => ({
  enabled: true, language: 'Go', status: 'watching', last_checked_at: '2026-10-27T11:00:00Z',
  lists: [], deferred_lists: 0, current: [], episodes: [], ...over,
})

function show(data: unknown, mutate = vi.fn()) {
  api.useTrending.mockReturnValue({ isPending: false, isError: false, data })
  api.useSetTrendingEnabled.mockReturnValue({ mutate, isPending: false, error: null })
  return mutate
}

afterEach(() => { cleanup(); vi.clearAllMocks() })

describe('StarsTrending', () => {
  it('shows current episodes with rank badges and a history', () => {
    const open = sighting()
    const goOpen = sighting({ id: 2, list_language: 'go', since: 'weekly', last_rank: 2, best_rank: 2 })
    const closed = sighting({ id: 3, left_at: '2026-10-26T12:00:00Z', first_seen_at: '2026-10-26T08:00:00Z', best_rank: 1, last_rank: 3 })
    show(status({ current: [open, goOpen], episodes: [open, goOpen, closed] }))
    render(<StarsTrending repoId={1} />)
    const now = within(screen.getByRole('region', { name: 'On trending now' }))
    expect(now.getByLabelText('All languages, today, rank 7')).toHaveTextContent('#7')
    expect(now.getByLabelText('Go, this week, rank 2')).toHaveTextContent('#2')
    const history = within(screen.getByRole('region', { name: 'History' }))
    expect(history.getAllByRole('listitem')).toHaveLength(1)
    expect(history.getByText('best #1')).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent(/^Watching\. Last checked /)
  })

  it('says so when the page cannot be read, without claiming the repository left', () => {
    const open = sighting()
    show(status({ status: 'unreadable', current: [open], episodes: [open] }))
    render(<StarsTrending repoId={1} />)
    expect(screen.getByRole('status')).toHaveTextContent("Can't read GitHub Trending right now")
    expect(screen.getByLabelText('All languages, today, rank 7')).toBeInTheDocument()
  })

  it('toggles the watcher for managers and hides the switch for readers', () => {
    const mutate = show(status({ enabled: false, status: 'off', last_checked_at: null }))
    const { rerender } = render(<StarsTrending repoId={1} />)
    expect(screen.getByRole('status')).toHaveTextContent('Not watching.')
    expect(screen.queryByText('On trending now')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('switch', { name: 'Watch GitHub Trending' }))
    expect(mutate).toHaveBeenCalledWith(true)
    rerender(<StarsTrending repoId={1} canManage={false} />)
    expect(screen.queryByRole('switch')).not.toBeInTheDocument()
  })

  it('notes postponed lists and the empty states', () => {
    show(status({ deferred_lists: 3 }))
    render(<StarsTrending repoId={1} />)
    expect(screen.getByText(/3 lists postponed/)).toBeInTheDocument()
    expect(screen.getByText('Not on any trending list right now.')).toBeInTheDocument()
    expect(screen.getByText('No appearances yet.')).toBeInTheDocument()
  })

  it('shows a loading state and a retryable error', () => {
    api.useSetTrendingEnabled.mockReturnValue({ mutate: vi.fn(), isPending: false, error: null })
    api.useTrending.mockReturnValue({ isPending: true, isError: false })
    const { rerender } = render(<StarsTrending repoId={1} />)
    expect(screen.getByLabelText('Loading trending status')).toBeInTheDocument()
    const refetch = vi.fn()
    api.useTrending.mockReturnValue({ isPending: false, isError: true, error: new Error('boom'), refetch })
    rerender(<StarsTrending repoId={1} />)
    expect(screen.getByText(/boom/)).toBeInTheDocument()
  })
})
