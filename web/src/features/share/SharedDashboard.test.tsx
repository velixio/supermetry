import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  ShareProblem,
  SharedDashboard,
  type ShareSummary,
} from './SharedDashboard'

const summary: ShareSummary = {
  sections: ['kpis', 'pages'],
  theme: 'system',
  date_label: 'Last 7 days',
  transparent_background: false,
  read_only: true,
  data: { totals: { visitors: 123, visits: 147, pageviews: 302 }, rows: [], columns: [] },
}

function renderDashboard(
  loadSummary = vi.fn().mockResolvedValue(summary),
  openShare = vi.fn().mockResolvedValue(undefined),
  loadSection = vi.fn().mockResolvedValue({ totals: {}, columns: ['page', 'visitors'], rows: [{ page: '/docs', visitors: 42 }] }),
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SharedDashboard
        slug="weekly-review"
        loadSummary={loadSummary}
        openShare={openShare}
        loadSection={loadSection}
      />
    </QueryClientProvider>,
  )
  return { loadSummary, openShare, loadSection }
}

afterEach(cleanup)

describe('shared dashboard', () => {
  it('renders a read-only report without application navigation', async () => {
    renderDashboard()

    expect(await screen.findByRole('heading', { name: 'Shared dashboard' })).toBeVisible()
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
    expect(screen.queryByText('Settings')).not.toBeInTheDocument()
    expect(await screen.findByText('123')).toBeVisible()
    expect(await screen.findByRole('heading', { name: 'Pages' })).toBeVisible()
    expect(await screen.findByText('/docs')).toBeVisible()
    expect(screen.queryByRole('heading', { name: 'Sources' })).not.toBeInTheDocument()
  })

  it('opens a password-protected share and then renders its data', async () => {
    const loadSummary = vi
      .fn()
      .mockRejectedValueOnce(
        new ShareProblem('share-password-required', 'Enter the dashboard password.'),
      )
      .mockResolvedValue(summary)
    const openShare = vi.fn().mockResolvedValue(undefined)
    renderDashboard(loadSummary, openShare)

    fireEvent.change(await screen.findByLabelText('Password'), {
      target: { value: 'correct horse' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Open dashboard' }))

    await waitFor(() =>
      expect(openShare).toHaveBeenCalledWith('weekly-review', 'correct horse'),
    )
    expect(await screen.findByText('123')).toBeVisible()
  })

  it('gives the traffic chart a legend and tabular text alternative', async () => {
    renderDashboard(
      vi.fn().mockResolvedValue({ ...summary, sections: ['chart'] }),
      undefined,
      vi.fn().mockResolvedValue({
        totals: {},
        columns: ['time', 'visitors', 'pageviews'],
        rows: [{ time: '2026-10-12', visitors: 12, pageviews: 24 }],
      }),
    )

    expect(await screen.findByRole('figure', { name: 'Visitors and pageviews over time' })).toBeVisible()
    expect(screen.getByText('Traffic trend values')).toBeInTheDocument()
    expect(screen.queryByLabelText('Analytics summary')).not.toBeInTheDocument()
  })

  it('shows the same unavailable state for inaccessible links without expiry details', async () => {
    renderDashboard(
      vi.fn().mockRejectedValue(
        new ShareProblem('share-unavailable', 'resource not found'),
      ),
    )

    expect(await screen.findByRole('heading', { name: 'Link unavailable' })).toBeVisible()
    expect(screen.getByRole('alert')).toHaveTextContent(
      'This shared dashboard is unavailable.',
    )
    expect(screen.queryByText(/expired/i)).not.toBeInTheDocument()
  })
})
