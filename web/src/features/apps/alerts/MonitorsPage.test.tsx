import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { MonitorsPage, describeSchedule } from './MonitorsPage'

const monitor = {
  id: 4, slug: 'nightly', environment: 'production', schedule_type: 'interval', schedule: '2 hour', timezone: 'UTC',
  checkin_margin_minutes: 5, max_runtime_minutes: 30, failure_threshold: 1, recovery_threshold: 1,
  status: 'missed', next_expected_at: '2026-11-20T12:00:00Z', last_checkin_at: '2026-11-20T10:00:00Z',
}

function renderPage(canEdit = true) {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MonitorsPage siteId={7} canEdit={canEdit} />
    </QueryClientProvider>,
  )
}

describe('MonitorsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    privateRequest.mockImplementation((method: string) => Promise.resolve(method === 'GET' ? { monitors: [monitor] } : monitor))
  })
  afterEach(cleanup)

  it('lists a monitor with its status and schedule, and disables it', async () => {
    renderPage()
    const card = await screen.findByRole('region', { name: 'Monitor nightly' })
    expect(within(card).getByText(/Missed · every 2 hours/)).toBeInTheDocument()
    fireEvent.click(within(card).getByRole('button', { name: 'Disable' }))
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('PATCH', '/api/private/sites/7/app/monitors/4', { body: { disabled: true } }))
  })

  it('hides the actions from viewers and shows the empty state', async () => {
    renderPage(false)
    expect(await screen.findByRole('region', { name: 'Monitor nightly' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument()
    cleanup()
    privateRequest.mockResolvedValue({ monitors: [] })
    renderPage()
    expect(await screen.findByText(/No monitors yet/)).toBeInTheDocument()
  })

  it('describes schedules', () => {
    expect(describeSchedule({ schedule_type: 'interval', schedule: '1 day', timezone: 'UTC' })).toBe('every day')
    expect(describeSchedule({ schedule_type: 'crontab', schedule: '*/5 * * * *', timezone: 'Europe/Berlin' })).toBe('*/5 * * * * (Europe/Berlin)')
  })
})
