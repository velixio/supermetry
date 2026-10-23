import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('@tanstack/react-router', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@tanstack/react-router')>()),
  Link: ({ to, params, children, ...rest }: { to: string; params?: Record<string, string>; children: React.ReactNode }) => (
    <a href={to.replace(/\$(\w+)/g, (_m, key: string) => params?.[key] ?? '').replace(/\/$/, '')} {...rest}>{children}</a>
  ),
}))

const api = vi.hoisted(() => ({
  useStarsSummary: vi.fn(), useStarsSeries: vi.fn(), useStarsEvents: vi.fn(), useStarsAnnotations: vi.fn(),
  useAddStarsAnnotation: vi.fn(), useDeleteStarsAnnotation: vi.fn(), useEraseStarsUser: vi.fn(),
  useStarsRepos: vi.fn(), useAddStarsRepo: vi.fn(), useUpdateStarsRepo: vi.fn(), useDeleteStarsRepo: vi.fn(),
  useRotateStarsSecret: vi.fn(), useSyncStarsRepo: vi.fn(),
}))
vi.mock('./githubStarsApi', async (importOriginal) => ({ ...(await importOriginal<typeof import('./githubStarsApi')>()), ...api }))

import { StarsRepoPage } from './StarsRepoPage'
import { markerBucket } from './StarsChart'

const point = (bucket: string, gained: number, lost: number, cumulative: number) => ({ bucket, gained, lost, net: gained - lost, cumulative })
const days = [
  point('2026-10-01T00:00:00+05:45', 1, 0, 2),
  point('2026-10-02T00:00:00+05:45', 1, 1, 2),
  point('2026-10-03T00:00:00+05:45', 0, 0, 2),
]

const mutation = (overrides: Record<string, unknown> = {}) => ({ mutate: vi.fn(), reset: vi.fn(), isPending: false, error: null, ...overrides })

const repo = (over: Record<string, unknown> = {}) => ({
  id: 1, repo: 'velixio/supermetry', enabled: true, time_zone: 'Asia/Kathmandu', has_token: true,
  webhook_url: 'https://stats.example.test/api/github/webhook/abcd1234efgh5678', public_count_url: 'https://stats.example.test/api/github/stars/velixio/supermetry', created_at: '2026-10-01T00:00:00Z',
  last_webhook_at: '2026-10-02T05:00:00Z', last_reconcile_at: '2026-10-03T00:00:00Z', last_error: null, ...over,
})

type Overrides = {
  repos?: unknown[]; events?: unknown; annotations?: unknown[]; erase?: ReturnType<typeof mutation>; add?: ReturnType<typeof mutation>
  remove?: ReturnType<typeof mutation>; addRepo?: ReturnType<typeof mutation>; updateRepo?: ReturnType<typeof mutation>
  deleteRepo?: ReturnType<typeof mutation>; rotate?: ReturnType<typeof mutation>; sync?: ReturnType<typeof mutation>
}

function ready(overrides: Overrides = {}) {
  api.useStarsRepos.mockReturnValue({ isPending: false, isError: false, data: { data: overrides.repos ?? [repo()] } })
  api.useAddStarsRepo.mockReturnValue(overrides.addRepo ?? mutation())
  api.useUpdateStarsRepo.mockReturnValue(overrides.updateRepo ?? mutation())
  api.useDeleteStarsRepo.mockReturnValue(overrides.deleteRepo ?? mutation())
  api.useRotateStarsSecret.mockReturnValue(overrides.rotate ?? mutation())
  api.useSyncStarsRepo.mockReturnValue(overrides.sync ?? mutation())
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

const removed = vi.fn()
function Detail({ owner = 'Velixio', name = 'SuperMetry' }: { owner?: string; name?: string }) {
  return <StarsRepoPage org="acme" owner={owner} name={name} onRemoved={removed} />
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

describe('StarsRepoPage', () => {
  it('shows the live counters and the accessible chart table', () => {
    ready()
    render(<Detail />)

    expect(screen.getByRole('heading', { level: 1, name: 'velixio/supermetry' })).toBeVisible()
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
    render(<Detail />)
    expect(api.useStarsSeries).toHaveBeenLastCalledWith(1, 'day')
    fireEvent.click(screen.getByRole('radio', { name: 'Hour' }))
    expect(api.useStarsSeries).toHaveBeenLastCalledWith(1, 'hour')
  })

  it('shows the gain per hour in hour mode and per day in day mode', () => {
    ready()
    const hours = [2, 0, 5, -1, 3].map((net, i) => ({ bucket: `2026-10-23T${String(9 + i).padStart(2, '0')}:00:00+05:30`, gained: Math.max(net, 0), lost: Math.max(-net, 0), net, cumulative: 100 + i }))
    const daily = [4, 10].map((net, i) => ({ bucket: `2026-10-${22 + i}T00:00:00+05:30`, gained: net, lost: 0, net, cumulative: 200 + i }))
    api.useStarsSeries.mockImplementation((_id: number, granularity: string) => ({ isPending: false, isError: false, data: { time_zone: 'Asia/Kolkata', data: granularity === 'hour' ? hours : daily } }))
    render(<Detail />)
    const day = screen.getByRole('group', { name: 'Gain per day' })
    expect(within(day).getByText('Today').nextSibling).toHaveTextContent('+10')
    expect(within(day).getByText('Yesterday').nextSibling).toHaveTextContent('+4')
    expect(within(day).getByText('Average per day').nextSibling).toHaveTextContent('+7')
    fireEvent.click(screen.getByRole('radio', { name: 'Hour' }))
    const hour = screen.getByRole('group', { name: 'Gain per hour' })
    expect(within(hour).getByText('This hour').nextSibling).toHaveTextContent('+3')
    expect(within(hour).getByText('Previous hour').nextSibling).toHaveTextContent('-1')
    expect(within(hour).getByText('Average per hour').nextSibling).toHaveTextContent('+1.8')
    expect(within(hour).getByText('Peak hour').nextSibling).toHaveTextContent('+5')
    expect(screen.queryByRole('group', { name: 'Gain per day' })).toBeNull()
  })

  it('reports other failures with a retry', () => {
    ready()
    const refetch = vi.fn()
    api.useStarsSummary.mockReturnValue({ isPending: false, isError: true, error: new Error('boom'), refetch })
    render(<Detail />)
    expect(screen.getByRole('alert', { name: '' })).toHaveTextContent('boom')
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(refetch).toHaveBeenCalled()
  })

  it('shows empty states for events and annotations', () => {
    ready({ annotations: [], events: { isSuccess: true, isError: false, hasNextPage: false, data: { pages: [{ data: [], next_cursor: null }] } } })
    render(<Detail />)
    expect(screen.getByText('No star events recorded yet.')).toBeVisible()
    expect(screen.getByText('No annotations yet.')).toBeVisible()
  })

  it('loads more events with the cursor', () => {
    const fetchNextPage = vi.fn()
    ready({ events: {
      isSuccess: true, isError: false, hasNextPage: true, isFetchingNextPage: false, fetchNextPage,
      data: { pages: [{ data: [{ id: 1, user_id: 1, login: 'alice', action: 'starred', occurred_at: '2026-10-02T03:00:00Z', source: 'webhook' }], next_cursor: 'c1' }] },
    } })
    render(<Detail />)
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }))
    expect(fetchNextPage).toHaveBeenCalled()
  })

  it('adds and deletes annotations', () => {
    const add = mutation()
    const remove = mutation()
    ready({ add, remove })
    render(<Detail />)
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
    render(<Detail />)
    fireEvent.click(screen.getByRole('button', { name: 'Erase events of alice' }))
    const dialog = screen.getByRole('alertdialog', { name: 'Erase events of alice?' })
    expect(erase.mutate).not.toHaveBeenCalled()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Erase events' }))
    expect(erase.mutate).toHaveBeenCalledWith('alice', expect.any(Object))
    expect(within(dialog).getByText(/future stars are ignored/)).toBeVisible()
  })

  it('validates the login before opening the erase dialog', () => {
    ready()
    render(<Detail />)
    const submit = screen.getByRole('button', { name: 'Erase user data' })
    expect(submit).toBeDisabled()
    fireEvent.change(screen.getByLabelText('GitHub login'), { target: { value: 'bad login' } })
    expect(submit).toBeDisabled()
    fireEvent.change(screen.getByLabelText('GitHub login'), { target: { value: 'carol' } })
    fireEvent.click(submit)
    expect(screen.getByRole('alertdialog', { name: 'Erase events of carol?' })).toBeVisible()
  })

  it('resolves the repository from the URL case-insensitively and links back to the overview', () => {
    ready({ repos: [repo(), repo({ id: 2, repo: 'velixio/docs' })] })
    render(<Detail />)
    expect(screen.getByRole('heading', { level: 1, name: 'velixio/supermetry' })).toBeVisible()
    expect(api.useStarsSummary).toHaveBeenLastCalledWith(1)
    expect(screen.getByRole('link', { name: 'All repositories' })).toHaveAttribute('href', '/app/acme/github-stars')
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
  })

  it('shows a not-found state with a link back for an unknown repository', () => {
    ready()
    render(<Detail name="missing" />)
    expect(screen.getByText('Repository not found')).toBeVisible()
    expect(screen.getByText('velixio/missing is not a tracked repository.')).toBeVisible()
    expect(screen.getByRole('link', { name: 'All repositories' })).toHaveAttribute('href', '/app/acme/github-stars')
    expect(screen.queryByRole('region', { name: /^Status of/ })).not.toBeInTheDocument()
  })

  it('shows the rotated secret on the repository page', () => {
    const rotate = mutation()
    rotate.mutate.mockImplementation((_id, options) => options.onSuccess({ repo: repo(), webhook_secret: 'rot', webhook_url: 'https://stats.example.test/x' }))
    ready({ rotate })
    render(<Detail />)
    fireEvent.click(screen.getByRole('button', { name: 'Rotate secret' }))
    fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Rotate secret' }))
    expect(screen.getByRole('region', { name: 'Webhook for velixio/supermetry' })).toBeVisible()
  })

  it('shows the status line and the last error', () => {
    ready({ repos: [repo({ last_error: 'github rate limit or access denied', last_webhook_at: null })] })
    render(<Detail />)
    const status = screen.getByRole('region', { name: 'Status of velixio/supermetry' })
    expect(within(status).getByText('none received yet')).toBeVisible()
    expect(within(status).getByRole('alert')).toHaveTextContent('github rate limit or access denied')
    expect(within(status).getByText('Asia/Kathmandu')).toBeVisible()
  })

  it('gives sibling sections distinct keys, so an update never duplicates the status card', () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    ready({ sync: mutation({ isSuccess: false }) })
    const { rerender } = render(<Detail />)
    ready({ sync: mutation({ isSuccess: true }) })
    rerender(<Detail />)
    expect(screen.getAllByRole('region', { name: 'Status of velixio/supermetry' })).toHaveLength(1)
    const duplicateKeys = error.mock.calls.filter((call) => String(call[0]).includes('same key'))
    error.mockRestore()
    expect(duplicateKeys).toHaveLength(0)
  })

  it('toggles enabled and starts a sync', () => {
    const updateRepo = mutation()
    const sync = mutation()
    ready({ updateRepo, sync })
    render(<Detail />)
    fireEvent.click(screen.getByRole('switch', { name: 'Enabled' }))
    expect(updateRepo.mutate).toHaveBeenCalledWith({ id: 1, patch: { enabled: false } })
    fireEvent.click(screen.getByRole('button', { name: 'Sync now' }))
    expect(sync.mutate).toHaveBeenCalledWith(1)
  })

  it('saves the time zone and can clear the token', () => {
    const updateRepo = mutation()
    ready({ updateRepo })
    render(<Detail />)
    fireEvent.click(screen.getByRole('button', { name: 'Settings' }))
    const dialog = screen.getByRole('dialog', { name: 'Settings for velixio/supermetry' })
    fireEvent.change(within(dialog).getByLabelText(/^Time zone/), { target: { value: 'UTC' } })
    fireEvent.click(within(dialog).getByRole('checkbox', { name: 'Remove the stored token' }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    expect(updateRepo.mutate).toHaveBeenCalledWith({ id: 1, patch: { time_zone: 'UTC', clear_token: true } }, expect.any(Object))
  })

  it('rotates the secret only after confirmation and shows the new one', () => {
    const rotate = mutation()
    rotate.mutate.mockImplementation((_id, options) => options.onSuccess({ repo: repo(), webhook_secret: 'newsecret', webhook_url: 'https://stats.example.test/x' }))
    ready({ rotate })
    render(<Detail />)
    fireEvent.click(screen.getByRole('button', { name: 'Rotate secret' }))
    expect(rotate.mutate).not.toHaveBeenCalled()
    fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Rotate secret' }))
    expect(rotate.mutate).toHaveBeenCalledWith(1, expect.any(Object))
    expect(screen.getByText('newsecret')).toBeVisible()
  })

  it('offers disable as the safe default and asks twice before removing data', () => {
    const deleteRepo = mutation()
    deleteRepo.mutate.mockImplementation((_vars, options) => options.onSuccess())
    ready({ deleteRepo })
    render(<Detail />)
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))
    const dialog = screen.getByRole('dialog', { name: 'Remove velixio/supermetry' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove repository and all its data' }))
    expect(deleteRepo.mutate).not.toHaveBeenCalled()
    const confirm = screen.getByRole('dialog', { name: 'Remove velixio/supermetry and all its data?' })
    fireEvent.click(within(confirm).getByRole('button', { name: 'Back' }))
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Disable and keep history' }))
    expect(deleteRepo.mutate).toHaveBeenCalledWith({ id: 1, deleteData: false }, expect.any(Object))
    expect(removed).not.toHaveBeenCalled() // a plain disable keeps the repository and the page
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))
    fireEvent.click(screen.getByRole('button', { name: 'Remove repository and all its data' }))
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Remove with data' }))
    expect(deleteRepo.mutate).toHaveBeenLastCalledWith({ id: 1, deleteData: true }, expect.any(Object))
    expect(removed).toHaveBeenCalledTimes(1) // removing with data returns to the overview
  })

  it('shows full numbers with thousands separators, never compact ones', () => {
    ready()
    api.useStarsSummary.mockReturnValue({
      isPending: false, isError: false, error: null,
      data: { stars: 82412, forks: 12345, watchers: 20000, open_issues: 10001, updated_at: '2026-10-03T00:00:00Z', net_24h: 15000, net_7d: -23456 },
    })
    render(<Detail />)
    for (const text of ['82,412', '12,345', '20,000', '10,001', '+15,000', '-23,456']) expect(screen.getByText(text)).toBeVisible()
    expect(screen.queryByText(/\d[.,]?\d*K/)).not.toBeInTheDocument()
  })

  it('shows the public count URL with a copy button', () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    ready()
    render(<Detail />)
    const status = screen.getByRole('region', { name: 'Status of velixio/supermetry' })
    expect(within(status).getByText('Public count URL')).toBeVisible()
    expect(within(status).getByText('https://stats.example.test/api/github/stars/velixio/supermetry')).toBeVisible()
    fireEvent.click(within(status).getByRole('button', { name: 'Copy Public count URL' }))
    expect(writeText).toHaveBeenCalledWith('https://stats.example.test/api/github/stars/velixio/supermetry')
  })
})
