import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const mutate = vi.fn()
const incident = { id: 6, alert_id: 8, site_id: 4, group_key: 'metric', severity: 'critical', opened_at: '2026-10-12T09:00:00Z', resolved_at: '2026-10-12T09:40:00Z', last_value: 3, peak_value: 17, threshold: 10, notified_at: '2026-10-12T09:01:00Z' }
vi.mock('@/features/apps/alerts/IncidentList', async (original) => ({
  ...await original<typeof import('@/features/apps/alerts/IncidentList')>(),
  useIncidents: (_site: number, _org: number, alertId?: number) => ({ data: alertId === 8 ? { data: [incident] } : undefined }),
}))
vi.mock('./notificationHistoryApi', async (original) => ({
  ...await original<typeof import('./notificationHistoryApi')>(),
  useNotificationHistory: () => ({ data: { data: [
    { id: 1, alert_id: 8, site_id: 4, channel_id: 2, channel_kind: 'email', channel_name: 'Owners', status: 'failed', attempts: 3, error_text: 'provider unavailable', fired_at: '2026-10-12T10:00:00Z', created_at: '2026-10-12T10:00:00Z', updated_at: '2026-10-12T10:00:00Z' },
    { id: 3, alert_id: 8, site_id: 4, channel_id: 0, channel_kind: 'none', channel_name: 'Silenced', status: 'suppressed', attempts: 0, error_text: 'silenced: deploy', fired_at: '2026-10-12T12:00:00Z', created_at: '2026-10-12T12:00:00Z', updated_at: '2026-10-12T12:00:00Z' },
    { id: 4, alert_id: 8, site_id: 4, channel_id: 5, channel_kind: 'pagerduty', channel_name: 'On call', status: 'succeeded', attempts: 1, fired_at: '2026-10-12T12:30:00Z', created_at: '2026-10-12T12:30:00Z', updated_at: '2026-10-12T12:30:00Z' },
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
    expect(screen.getAllByText('succeeded').length).toBe(2)
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

  it('labels suppressed and PagerDuty rows, and shows the incidents of a filtered rule', () => {
    render(<NotificationHistoryPage siteId={4} orgId={3} filters={{ alertId: 8 }} onFilters={() => undefined} />)
    expect(screen.getByText('suppressed')).toBeVisible()
    expect(screen.getByText('Silenced')).toBeVisible()
    expect(screen.getAllByText('Suppressed').length).toBeGreaterThan(0)
    expect(screen.getByText('PagerDuty', { selector: 'span' })).toBeVisible()
    const incidents = screen.getByRole('list', { name: 'Incidents' })
    expect(incidents).toHaveTextContent('Resolved')
    expect(incidents).toHaveTextContent('Peak 17')
  })

  it('filters the list to one rule from its alert link', () => {
    const onFilters = vi.fn()
    render(<NotificationHistoryPage siteId={4} orgId={3} filters={{}} onFilters={onFilters} />)
    fireEvent.click(screen.getByRole('button', { name: 'Alert 9' }))
    expect(onFilters).toHaveBeenCalledWith({ alertId: 9 })
  })
})
