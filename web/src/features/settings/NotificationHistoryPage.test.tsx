import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const mutate = vi.fn()
vi.mock('./notificationHistoryApi', async (original) => ({
  ...await original<typeof import('./notificationHistoryApi')>(),
  useNotificationHistory: () => ({ data: { data: [
    { id: 1, alert_id: 8, site_id: 4, channel_id: 2, channel_kind: 'email', channel_name: 'Owners', status: 'failed', attempts: 3, error_text: 'provider unavailable', fired_at: '2026-10-12T10:00:00Z', created_at: '2026-10-12T10:00:00Z', updated_at: '2026-10-12T10:00:00Z' },
    { id: 2, alert_id: 9, site_id: 4, channel_id: 3, channel_kind: 'slack', channel_name: 'Incidents', status: 'succeeded', attempts: 1, fired_at: '2026-10-12T11:00:00Z', created_at: '2026-10-12T11:00:00Z', updated_at: '2026-10-12T11:00:00Z' },
  ] }, isPending: false, error: null }),
  useRetryNotification: () => ({ mutate, isPending: false, error: null }),
}))

import { NotificationHistoryPage } from './NotificationHistoryPage'

describe('NotificationHistoryPage', () => {
  beforeEach(() => mutate.mockClear())
  afterEach(cleanup)

  it('shows text statuses and retries only failed deliveries', () => {
    render(<NotificationHistoryPage siteId={4} orgId={3} filters={{}} onFilters={() => undefined} />)
    expect(screen.getByText('failed')).toBeVisible()
    expect(screen.getByText('succeeded')).toBeVisible()
    expect(screen.getAllByRole('button', { name: 'Retry delivery' })).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: 'Retry delivery' }))
    expect(mutate).toHaveBeenCalledWith(1)
  })

  it('reports filter changes to URL state owner', () => {
    const onFilters = vi.fn()
    render(<NotificationHistoryPage siteId={4} orgId={3} filters={{}} onFilters={onFilters} />)
    fireEvent.change(screen.getByLabelText('Status'), { target: { value: 'failed' } })
    expect(onFilters).toHaveBeenCalledWith({ status: 'failed' })
  })
})
