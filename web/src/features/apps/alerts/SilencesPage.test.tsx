import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { SilencesPage } from './SilencesPage'

const rule = { id: 5, site_id: 7, kind: 'app_rule', name: 'Crash rate', channel_ids: [], enabled: true, threshold: 0, created_at: '2026-11-01T00:00:00Z' }
const silence = { id: 3, site_id: 7, kind: 'maintenance', weekdays: [0], start_time: '02:00', duration_minutes: 120, tz: 'Europe/Berlin', starts_at: '2026-11-01T00:00:00Z', reason: 'weekly deploy', created_at: '2026-11-01T00:00:00Z' }

function renderPage(canEdit = true) {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <SilencesPage siteId={7} orgId={3} siteTimezone="Europe/Berlin" canEdit={canEdit} />
    </QueryClientProvider>,
  )
}

describe('SilencesPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    privateRequest.mockImplementation((method: string, path: string) => {
      if (method === 'GET') return Promise.resolve({ data: path.endsWith('/alerts') ? [rule] : [silence] })
      return Promise.resolve({})
    })
  })
  afterEach(cleanup)

  it('lists silences and deletes one', async () => {
    renderPage()
    await screen.findByText(/weekly deploy/)
    const list = screen.getByRole('list', { name: 'Silences' })
    expect(within(list).getByText('All rules')).toBeInTheDocument()
    expect(within(list).getByText(/Every Sun at 02:00 for 120 min \(Europe\/Berlin\) · weekly deploy/)).toBeInTheDocument()
    fireEvent.click(within(list).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('DELETE', '/api/private/sites/7/alert-silences/3', { orgId: 3 }))
  })

  it('builds a weekly maintenance window and previews the next two occurrences in the site zone', async () => {
    renderPage()
    await screen.findByText(/weekly deploy/)
    await screen.findByRole('option', { name: 'Crash rate' })
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'maintenance' } })
    fireEvent.change(screen.getByLabelText('Applies to'), { target: { value: '5' } })
    const preview = screen.getByRole('status', { name: 'Next occurrences' })
    expect(within(preview).getByText('Next occurrences (Europe/Berlin)')).toBeInTheDocument()
    expect(within(preview).getAllByRole('listitem')).toHaveLength(2)
    expect(preview).toHaveTextContent('Sun')
    expect(preview).toHaveTextContent('02:00')
    fireEvent.click(screen.getByRole('button', { name: 'Create silence' }))
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('POST', '/api/private/sites/7/alert-silences', {
      orgId: 3,
      body: { kind: 'maintenance', reason: '', alert_id: 5, weekdays: [0], start_time: '02:00', duration_minutes: 120, tz: 'Europe/Berlin' },
    }))
  })

  it('hides the form and delete from viewers', async () => {
    renderPage(false)
    await screen.findByText(/weekly deploy/)
    expect(screen.queryByRole('button', { name: 'Create silence' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument()
  })
})
