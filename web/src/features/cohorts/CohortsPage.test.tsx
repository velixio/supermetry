import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { AuthorizationProvider } from '@/lib/authorization'
import { renderWithRouter } from '@/test/renderWithRouter'

const mocks = vi.hoisted(() => ({
  useCohorts: vi.fn(),
  useSaveCohort: vi.fn(),
  useDuplicateCohort: vi.fn(),
  useDeleteCohort: vi.fn(),
  useCohortEvaluation: vi.fn(),
  usePowerUsers: vi.fn(),
}))
vi.mock('./api', () => mocks)

import { CohortsPage } from './CohortsPage'

const saved = {
  id: 4, site_id: 7, name: 'Pro signups', created_at: '2026-10-01T10:00:00Z', updated_at: '2026-10-01T10:00:00Z',
  definition: { match: 'all', window_days: 30, conditions: [{ type: 'event', name: 'signup', min_count: 2, negate: false }] },
}
const mutation = (overrides: Record<string, unknown> = {}) => ({ mutate: vi.fn(), mutateAsync: vi.fn(), reset: vi.fn(), isPending: false, isError: false, error: null, ...overrides })
const range = { from: '2026-10-01', to: '2026-10-10' }

async function show(capabilities: string[] = ['profiles.read'], props: Partial<Parameters<typeof CohortsPage>[0]> = {}) {
  const view = renderWithRouter(
    <AuthorizationProvider membership={{ effective_capabilities: capabilities }}>
      <CohortsPage canEdit dateRange={range} orgSlug="acme" siteId={7} siteSlug="docs" {...props} />
    </AuthorizationProvider>,
  )
  await screen.findByRole('heading', { level: 1 })
  return view
}

beforeEach(() => {
  mocks.useCohorts.mockReturnValue({ data: [saved], isPending: false, isError: false })
  mocks.useSaveCohort.mockReturnValue(mutation())
  mocks.useDuplicateCohort.mockReturnValue(mutation())
  mocks.useDeleteCohort.mockReturnValue(mutation())
  mocks.useCohortEvaluation.mockReturnValue({ isPending: true, isFetching: true, isError: false })
  mocks.usePowerUsers.mockReturnValue({
    isPending: false, isError: false,
    data: { metric: 'sessions', min: 2, qualifying: 2, qualifying_capped: false, data: [
      { id: 1, external_id: 'alice', first_seen_at: '2026-09-01T00:00:00Z', last_seen_at: '2026-10-10T11:00:00Z', sessions: 3, events: 6 },
      { id: 2, external_id: 'bob', first_seen_at: '2026-10-08T00:00:00Z', last_seen_at: '2026-10-09T15:00:00Z', sessions: 2, events: 3 },
    ] },
  })
})
afterEach(cleanup)

describe('CohortsPage', () => {
  it('explains the policy instead of building audiences in anonymous mode', async () => {
    await show(['profiles.read'], { policy: 'anonymous' })
    expect(screen.getByRole('heading', { level: 1, name: 'Cohorts need identified tracking' })).toBeVisible()
    expect(screen.getByText(/keeps no person-level history/i)).toBeVisible()
    expect(mocks.useCohorts).not.toHaveBeenCalled()
  })

  it('explains that profiles must be enabled', async () => {
    await show(['profiles.read'], { policy: 'profiles_disabled' })
    expect(screen.getByRole('heading', { name: 'Profiles are disabled for this site' })).toBeVisible()
  })

  it('lists cohorts in plain language with accessible actions and a dashboard filter link', async () => {
    await show()
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1)
    const row = screen.getByRole('row', { name: /Pro signups/ })
    expect(within(row).getByText('Did “signup” 2+ times')).toBeVisible()
    expect(within(row).getByText('30 days')).toBeVisible()
    for (const name of ['Evaluate Pro signups', 'Edit Pro signups', 'Duplicate Pro signups', 'Delete Pro signups']) {
      expect(within(row).getByRole('button', { name })).toBeVisible()
    }
    const link = within(row).getByRole('link', { name: 'Use Pro signups as a dashboard filter' })
    expect(link.getAttribute('href')).toContain('cohort')
  })

  it('hides write actions from viewers', async () => {
    await show(['profiles.read'], { canEdit: false })
    expect(screen.queryByRole('button', { name: 'New cohort' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Delete Pro signups' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Evaluate Pro signups' })).toBeVisible()
  })

  it('shows the size and sample of an evaluated cohort, and withholds the sample without permission', async () => {
    await show()
    mocks.useCohortEvaluation.mockReturnValue({
      isPending: false, isFetching: false, isError: false,
      data: { size: 2, size_capped: false, window_from: '2026-09-10T12:00:00Z', window_to: '2026-10-10T12:00:00Z', sample_withheld: false,
        sample: [{ id: 1, external_id: 'alice', first_seen_at: '2026-09-01T00:00:00Z', last_seen_at: '2026-10-10T11:00:00Z', sessions: 3, events: 6 }] },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Evaluate Pro signups' }))
    const result = screen.getByRole('region', { name: /Result: Pro signups/ })
    expect(within(result).getByText('2')).toBeVisible()
    expect(within(result).getByRole('cell', { name: 'alice' })).toBeVisible()

    cleanup()
    mocks.useCohortEvaluation.mockReturnValue({
      isPending: false, isFetching: false, isError: false,
      data: { size: 2, size_capped: false, window_from: '2026-09-10T12:00:00Z', window_to: '2026-10-10T12:00:00Z', sample_withheld: true, sample: [] },
    })
    await show(['profiles.read'])
    fireEvent.click(screen.getByRole('button', { name: 'Evaluate Pro signups' }))
    expect(screen.getByText(/needs permission to read profiles/i)).toBeVisible()
    expect(screen.queryByRole('cell', { name: 'alice' })).not.toBeInTheDocument()
  })

  it('builds a cohort with the keyboard-reachable form, validates, then saves the definition', async () => {
    const mutateAsync = vi.fn().mockResolvedValue({ ...saved, id: 9, name: 'Docs readers' })
    mocks.useSaveCohort.mockReturnValue(mutation({ mutateAsync }))
    await show()
    fireEvent.click(screen.getByRole('button', { name: 'New cohort' }))
    const form = screen.getByRole('form', { name: 'New cohort' })

    fireEvent.click(within(form).getByRole('button', { name: 'Save cohort' }))
    expect(await within(form).findByRole('alert')).toHaveTextContent('Name must be between 1 and 80 characters.')
    expect(mutateAsync).not.toHaveBeenCalled()

    fireEvent.change(within(form).getByLabelText('Name'), { target: { value: 'Docs readers' } })
    fireEvent.change(within(form).getByLabelText('Type'), { target: { value: 'page' } })
    fireEvent.change(within(form).getByLabelText('Path match'), { target: { value: 'starts_with' } })
    fireEvent.change(within(form).getByLabelText('Page path'), { target: { value: '/docs' } })
    fireEvent.change(within(form).getByLabelText('At least this many times'), { target: { value: '2' } })
    fireEvent.click(within(form).getByLabelText('Did not (invert this condition)'))
    fireEvent.click(within(form).getByRole('button', { name: 'Add condition' }))
    expect(within(form).getByText('Condition 2')).toBeVisible()
    fireEvent.click(within(form).getByRole('button', { name: 'Remove condition 2' }))
    expect(within(form).queryByText('Condition 2')).not.toBeInTheDocument()
    fireEvent.click(within(form).getByRole('button', { name: 'Save cohort' }))

    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1))
    expect(mutateAsync).toHaveBeenCalledWith({
      id: undefined,
      name: 'Docs readers',
      definition: { match: 'all', window_days: 30, conditions: [{ type: 'page', negate: true, path: '/docs', operator: 'starts_with', min_count: 2 }] },
    })
  })

  it('shows server errors next to the form', async () => {
    mocks.useSaveCohort.mockReturnValue(mutation({ isError: true, error: new Error('a cohort with this name already exists') }))
    await show()
    fireEvent.click(screen.getByRole('button', { name: 'New cohort' }))
    expect(screen.getByRole('alert')).toHaveTextContent('a cohort with this name already exists')
  })

  it('confirms before deleting', async () => {
    const mutateAsync = vi.fn().mockResolvedValue(undefined)
    mocks.useDeleteCohort.mockReturnValue(mutation({ mutateAsync }))
    await show()
    fireEvent.click(screen.getByRole('button', { name: 'Delete Pro signups' }))
    expect(await screen.findByText(/Delete “Pro signups”\?/)).toBeVisible()
    expect(mutateAsync).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Delete cohort' }))
    await waitFor(() => expect(mutateAsync).toHaveBeenCalledWith(4))
  })

  it('lists power users behind the profiles permission and saves a threshold as a cohort', async () => {
    await show()
    fireEvent.mouseDown(screen.getByRole('tab', { name: 'Power users' }), { button: 0 })
    fireEvent.click(screen.getByRole('tab', { name: 'Power users' }))
    expect(await screen.findByRole('cell', { name: 'alice' })).toBeVisible()
    expect(screen.getByText('Profiles reaching the threshold: 2')).toBeVisible()
    fireEvent.change(screen.getByLabelText('At least'), { target: { value: '5' } })
    expect(mocks.usePowerUsers).toHaveBeenLastCalledWith(7, range, 'sessions', 5)
    fireEvent.click(screen.getByRole('button', { name: 'Save as cohort' }))
    const form = await screen.findByRole('form', { name: 'New cohort' })
    expect(within(form).getByLabelText('Count')).toHaveValue('sessions')
    expect(within(form).getByLabelText('At least this many times')).toHaveValue('5')
  })

  it('denies the power users tab without the profiles permission', async () => {
    await show([])
    fireEvent.mouseDown(screen.getByRole('tab', { name: 'Power users' }), { button: 0 })
    fireEvent.click(screen.getByRole('tab', { name: 'Power users' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/permission to read profiles/i)
    expect(mocks.usePowerUsers).not.toHaveBeenCalled()
  })

  it('renders the empty state', async () => {
    mocks.useCohorts.mockReturnValue({ data: [], isPending: false, isError: false })
    await show()
    expect(screen.getByText('No cohorts yet')).toBeVisible()
  })
})
