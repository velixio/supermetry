import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiProblem } from '@/lib/apiProblem'

const api = vi.hoisted(() => ({
  useStarsSummary: vi.fn(), useStarsSeries: vi.fn(), useStarsEvents: vi.fn(), useStarsAnnotations: vi.fn(),
  useAddStarsAnnotation: vi.fn(), useDeleteStarsAnnotation: vi.fn(), useEraseStarsUser: vi.fn(),
}))
vi.mock('./githubStarsApi', async (importOriginal) => ({ ...(await importOriginal<typeof import('./githubStarsApi')>()), ...api }))

import { AdminGitHubStarsPage } from './AdminGitHubStarsPage'
import { markerBucket } from './StarsChart'

const point = (bucket: string, gained: number, lost: number, cumulative: number) => ({ bucket, gained, lost, net: gained - lost, cumulative })
const days = [
  point('2026-10-01T00:00:00+05:45', 1, 0, 2),
  point('2026-10-02T00:00:00+05:45', 1, 1, 2),
  point('2026-10-03T00:00:00+05:45', 0, 0, 2),
]

const mutation = (overrides: Record<string, unknown> = {}) => ({ mutate: vi.fn(), reset: vi.fn(), isPending: false, error: null, ...overrides })

function ready(overrides: { events?: unknown; annotations?: unknown[]; erase?: ReturnType<typeof mutation>; add?: ReturnType<typeof mutation>; remove?: ReturnType<typeof mutation> } = {}) {
  api.useStarsSummary.mockReturnValue({
    isPending: false, isError: false, error: null,
    data: { stars: 1234, forks: 5, watchers: 6, open_issues: 7, updated_at: '2026-10-03T00:00:00Z', net_24h: 3, net_7d: -2 },
  })
  api.useStarsSeries.mockReturnValue({ isPending: false, isError: false, data: { time_zone: 'Asia/Kathmandu', data: days } })
  api.useStarsAnnotations.mockReturnValue({ data: { data: overrides.annotations ?? [{ id: 9, at: '2026-10-02T04:00:00Z', label: 'Launch', created_by: 1 }] } })
  api.useStarsEvents.mockReturnValue(overrides.events ?? {
    isSuccess: true, isError: false, hasNextPage: true, isFetchingNextPage: false, fetchNextPage: vi.fn(),
    data: { pages: [{ data: [
      { id: 2, user_id: 2, login: 'bob', action: 'unstarred', occurred_at: '2026-10-02T05:00:00Z', source: 'webhook' },
      { id: 1, user_id: 1, login: 'alice', action: 'starred', occurred_at: '2026-10-02T03:00:00Z', source: 'backfill' },
    ], next_cursor: 'c1' }] },
  })
  api.useAddStarsAnnotation.mockReturnValue(overrides.add ?? mutation())
  api.useDeleteStarsAnnotation.mockReturnValue(overrides.remove ?? mutation())
  api.useEraseStarsUser.mockReturnValue(overrides.erase ?? mutation())
}

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('markerBucket', () => {
  it('maps an instant to the bucket that contains it', () => {
    expect(markerBucket(days, 'day', '2026-10-02T04:00:00Z')).toBe(days[1].bucket)
    expect(markerBucket(days, 'day', '2026-10-03T10:00:00Z')).toBe(days[2].bucket)
  })
  it('ignores instants outside the range', () => {
    expect(markerBucket(days, 'day', '2026-09-01T00:00:00Z')).toBeUndefined()
    expect(markerBucket(days, 'day', '2026-10-05T00:00:00Z')).toBeUndefined()
    expect(markerBucket([], 'day', '2026-10-02T00:00:00Z')).toBeUndefined()
  })
})

describe('AdminGitHubStarsPage', () => {
  it('shows the live counters and the accessible chart table', () => {
    ready()
    render(<AdminGitHubStarsPage />)

    expect(screen.getByRole('heading', { level: 1, name: 'GitHub stars' })).toBeVisible()
    expect(screen.getByText('1,234')).toBeVisible()
    expect(screen.getByText('+3')).toBeVisible()
    expect(screen.getByText('-2')).toBeVisible()
    const table = screen.getByRole('table', { name: 'Star changes per bucket' })
    const rows = within(table).getAllByRole('row')
    expect(rows).toHaveLength(4)
    expect(rows[2]).toHaveTextContent('Marker: Launch')
    expect(within(rows[2]).getAllByRole('cell').map((cell) => cell.textContent)).toEqual(['1', '1', '2'])
  })

  it('switches the chart between hour and day buckets', () => {
    ready()
    render(<AdminGitHubStarsPage />)
    expect(api.useStarsSeries).toHaveBeenLastCalledWith('day')
    fireEvent.click(screen.getByRole('radio', { name: 'Hour' }))
    expect(api.useStarsSeries).toHaveBeenLastCalledWith('hour')
  })

  it('shows a not-configured state for the 404 the API returns when the feature is off', () => {
    ready()
    api.useStarsSummary.mockReturnValue({ isPending: false, isError: true, error: new ApiProblem(404, { detail: 'GitHub star tracking is not configured' }) })
    render(<AdminGitHubStarsPage />)
    expect(screen.getByText('GitHub star tracking is off')).toBeVisible()
    expect(screen.getByText(/SUPERMETRY_GITHUB_STARS_REPO/)).toBeVisible()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('reports other failures with a retry', () => {
    ready()
    const refetch = vi.fn()
    api.useStarsSummary.mockReturnValue({ isPending: false, isError: true, error: new Error('boom'), refetch })
    render(<AdminGitHubStarsPage />)
    expect(screen.getByRole('alert')).toHaveTextContent('boom')
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(refetch).toHaveBeenCalled()
  })

  it('shows empty states for events and annotations', () => {
    ready({ annotations: [], events: { isSuccess: true, isError: false, hasNextPage: false, data: { pages: [{ data: [], next_cursor: null }] } } })
    render(<AdminGitHubStarsPage />)
    expect(screen.getByText('No star events recorded yet.')).toBeVisible()
    expect(screen.getByText('No annotations yet.')).toBeVisible()
  })

  it('loads more events with the cursor', () => {
    const fetchNextPage = vi.fn()
    ready({ events: {
      isSuccess: true, isError: false, hasNextPage: true, isFetchingNextPage: false, fetchNextPage,
      data: { pages: [{ data: [{ id: 1, user_id: 1, login: 'alice', action: 'starred', occurred_at: '2026-10-02T03:00:00Z', source: 'webhook' }], next_cursor: 'c1' }] },
    } })
    render(<AdminGitHubStarsPage />)
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }))
    expect(fetchNextPage).toHaveBeenCalled()
  })

  it('adds and deletes annotations', () => {
    const add = mutation()
    const remove = mutation()
    ready({ add, remove })
    render(<AdminGitHubStarsPage />)
    const submit = screen.getByRole('button', { name: 'Add annotation' })
    expect(submit).toBeDisabled()
    fireEvent.change(screen.getByLabelText('Label'), { target: { value: '  Show HN  ' } })
    fireEvent.change(screen.getByLabelText('Date and time'), { target: { value: '2026-10-02T10:30' } })
    fireEvent.click(submit)
    expect(add.mutate).toHaveBeenCalledWith({ at: new Date('2026-10-02T10:30').toISOString(), label: 'Show HN' }, expect.any(Object))
    fireEvent.click(screen.getByRole('button', { name: 'Delete annotation Launch' }))
    expect(remove.mutate).toHaveBeenCalledWith(9)
  })

  it('erases a user only after confirmation', () => {
    const erase = mutation()
    ready({ erase })
    render(<AdminGitHubStarsPage />)
    fireEvent.click(screen.getByRole('button', { name: 'Erase events of alice' }))
    const dialog = screen.getByRole('alertdialog', { name: 'Erase events of alice?' })
    expect(erase.mutate).not.toHaveBeenCalled()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Erase events' }))
    expect(erase.mutate).toHaveBeenCalledWith('alice', expect.any(Object))
  })

  it('validates the login before opening the erase dialog', () => {
    ready()
    render(<AdminGitHubStarsPage />)
    const submit = screen.getByRole('button', { name: 'Erase user data' })
    expect(submit).toBeDisabled()
    fireEvent.change(screen.getByLabelText('GitHub login'), { target: { value: 'bad login' } })
    expect(submit).toBeDisabled()
    fireEvent.change(screen.getByLabelText('GitHub login'), { target: { value: 'carol' } })
    fireEvent.click(submit)
    expect(screen.getByRole('alertdialog', { name: 'Erase events of carol?' })).toBeVisible()
  })
})
