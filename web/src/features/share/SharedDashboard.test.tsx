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
  totals: {
    visitors: 123,
    visits: 147,
    pageviews: 302,
  },
}

function renderDashboard(
  loadSummary = vi.fn().mockResolvedValue(summary),
  openShare = vi.fn().mockResolvedValue(undefined),
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
      />
    </QueryClientProvider>,
  )
  return { loadSummary, openShare }
}

afterEach(cleanup)

describe('shared dashboard', () => {
  it('renders a read-only report without application navigation', async () => {
    renderDashboard()

    expect(await screen.findByRole('heading', { name: 'Shared dashboard' })).toBeVisible()
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
    expect(screen.queryByText('Settings')).not.toBeInTheDocument()
    expect(await screen.findByText('123')).toBeVisible()
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
