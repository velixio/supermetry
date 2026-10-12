import { renderAuthorized as render } from '@/test/authorization'
import '@testing-library/jest-dom/vitest'

import { act, cleanup, fireEvent, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { mutate, useReplays, useSetReplaySaved } = vi.hoisted(() => ({ mutate: vi.fn(), useReplays: vi.fn(), useSetReplaySaved: vi.fn() }))
vi.mock('@/features/replays/api', () => ({ useReplays, useSetReplaySaved }))
vi.mock('./ReplayLink', () => ({ ReplayLink: () => <span>Play replay</span> }))

import { ReplaysPage } from './ReplaysPage'

const replay = {
  session_id: '401', started_at: '2026-10-08T08:00:00Z', duration_s: 75,
  pages: 3, bytes: 1024, trigger: 'sampled', country: 'IN', city: 'Bengaluru',
  browser: 'Chrome', os: 'Android', device: 'mobile', actions: 12, saved: false,
}

function props(overrides: Record<string, unknown> = {}) {
  return {
    siteId: 7, org: 'acme', site: 'example', from: '2026-10-01', to: '2026-10-08', page: 1,
    filters: {}, layout: 'table' as const, onFiltersChange: vi.fn(), onLayoutChange: vi.fn(),
    onFirst: vi.fn(), onNext: vi.fn(), onPrevious: vi.fn(), ...overrides,
  }
}

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.clearAllMocks()
})

describe('ReplaysPage', () => {
  it('renders visitor detail columns and replay activity', () => {
    useReplays.mockReturnValue({
      data: {
        items: [replay], total: 1, total_capped: false,
      },
      isPending: false,
      isError: false,
      isFetching: false,
    })
    useSetReplaySaved.mockReturnValue({ mutate, isPending: false, isError: false })

    render(<ReplaysPage {...props()} />)

    for (const name of ['Saved', 'Play', 'Started', 'Location', 'Browser', 'OS', 'Device', 'Actions', 'Duration', 'Pages', 'Trigger']) {
      expect(screen.getByRole('columnheader', { name })).toBeVisible()
    }
    expect(screen.getByRole('list', { name: 'Replay cards' })).toBeInTheDocument()
    expect(screen.getAllByText('Bengaluru, India')).toHaveLength(2)
    expect(screen.queryByText('IN')).not.toBeInTheDocument()
    expect(screen.getAllByText('Chrome')).toHaveLength(2)
    expect(screen.getAllByText('Android')).toHaveLength(2)
    expect(screen.getAllByText('Mobile')).toHaveLength(2)
    expect(screen.getByRole('cell', { name: '12' })).toBeVisible()
    fireEvent.click(screen.getAllByRole('button', { name: 'Save replay' })[0])
    expect(mutate).toHaveBeenCalledWith({ sessionId: '401', saved: true })
  })

  it('debounces search into URL filter state and exposes keyboard-native duration controls', () => {
    vi.useFakeTimers()
    useReplays.mockReturnValue({ data: { items: [replay], total: 1, total_capped: false }, isPending: false, isError: false, isFetching: false })
    useSetReplaySaved.mockReturnValue({ mutate, isPending: false, isError: false })
    const onFiltersChange = vi.fn()
    render(<ReplaysPage {...props({ onFiltersChange })} />)

    fireEvent.change(screen.getByRole('searchbox', { name: 'Search replays' }), { target: { value: 'checkout' } })
    act(() => vi.advanceTimersByTime(299))
    expect(onFiltersChange).not.toHaveBeenCalled()
    act(() => vi.advanceTimersByTime(1))
    expect(onFiltersChange).toHaveBeenCalledWith({ q: 'checkout' })

    fireEvent.change(screen.getByRole('spinbutton', { name: 'Minimum duration in seconds' }), { target: { value: '30' } })
    expect(onFiltersChange).toHaveBeenCalledWith({ min_duration: 30 })
  })

  it('offers next actions for saved-empty and error states', () => {
    const onFiltersChange = vi.fn()
    const refetch = vi.fn()
    useSetReplaySaved.mockReturnValue({ mutate, isPending: false, isError: false })
    useReplays.mockReturnValueOnce({ data: { items: [], total: 0, total_capped: false }, isPending: false, isError: false, isFetching: false })
    const view = render(<ReplaysPage {...props({ filters: { saved: true }, onFiltersChange })} />)
    fireEvent.click(screen.getByRole('button', { name: 'Show all replays' }))
    expect(onFiltersChange).toHaveBeenCalledWith({ saved: undefined })

    view.unmount()
    useReplays.mockReturnValue({ error: { message: 'Replay list unavailable' }, isPending: false, isError: true, isFetching: false, refetch })
    render(<ReplaysPage {...props()} />)
    expect(screen.getByRole('alert')).toHaveTextContent('Replay list unavailable')
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(refetch).toHaveBeenCalled()
  })
})
