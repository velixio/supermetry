import '@testing-library/jest-dom/vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))

vi.mock('@/lib/api', () => ({ api: { POST: post } }))

import { VitalsPage } from './VitalsPage'

const response = {
  overall: {
    lcp: { p75: 2800, samples: 20, good: 0.6, needs_improvement: 0.25, poor: 0.15 },
    cls: { p75: 80, samples: 18, good: 0.8, needs_improvement: 0.15, poor: 0.05 },
    inp: { p75: 550, samples: 12, good: 0.25, needs_improvement: 0.5, poor: 0.25 },
  },
  breakdown: [
    {
      value: '/pricing',
      lcp: { p75: 3100, samples: 10, good: 0.5, needs_improvement: 0.3, poor: 0.2 },
      cls: { p75: 120, samples: 9, good: 0.7, needs_improvement: 0.2, poor: 0.1 },
      inp: { p75: 220, samples: 7, good: 0.4, needs_improvement: 0.4, poor: 0.2 },
    },
  ],
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <VitalsPage
        siteId={7}
        dateRange={{ from: '2026-09-01', to: '2026-09-30' }}
        filters={[{ dimension: 'country', operator: 'is', values: ['IN'] }]}
      />
    </QueryClientProvider>,
  )
}

describe('Web Vitals page', () => {
  beforeEach(() => {
    post.mockResolvedValue({
      data: response,
      response: new Response(null, { status: 200 }),
    })
  })

  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('shows p75 ratings and distributions from the filtered period', async () => {
    renderPage()

    expect(await screen.findByRole('heading', { name: 'Web Vitals' })).toBeVisible()
    expect(await screen.findByRole('region', { name: 'Largest Contentful Paint' })).toHaveTextContent('2.8 s')
    expect(screen.getByRole('region', { name: 'Cumulative Layout Shift' })).toHaveTextContent('0.080')
    expect(screen.getByRole('region', { name: 'Interaction to Next Paint' })).toHaveTextContent('550 ms')
    expect(screen.getByRole('region', { name: 'Interaction to Next Paint' })).toHaveTextContent('Poor')

    const distribution = within(
      screen.getByRole('region', { name: 'Largest Contentful Paint' }),
    ).getByRole('img', { name: '60% good, 25% needs improvement, 15% poor' })
    expect(distribution).toBeVisible()

    await waitFor(() => expect(post).toHaveBeenCalled())
    expect(post.mock.calls[0]).toEqual([
      '/api/private/vitals/query',
      {
        body: {
          site_id: 7,
          date_range: { from: '2026-09-01', to: '2026-09-30' },
          filters: [{ dimension: 'country', operator: 'is', values: ['IN'] }],
          dimension: 'page',
        },
      },
    ])
  })

  it('switches the breakdown from pages to devices', async () => {
    renderPage()
    expect(await screen.findByRole('cell', { name: '/pricing' })).toBeVisible()

    const device = screen.getByRole('tab', { name: 'Device' })
    fireEvent.mouseDown(device, { button: 0 })
    fireEvent.click(device)

    await waitFor(() =>
      expect(post).toHaveBeenLastCalledWith('/api/private/vitals/query', {
        body: expect.objectContaining({ dimension: 'device' }),
      }),
    )
  })

  it('explains that empty reports need the full tracker', async () => {
    post.mockResolvedValue({
      data: {
        overall: {
          lcp: { samples: 0, good: 0, needs_improvement: 0, poor: 0 },
          cls: { samples: 0, good: 0, needs_improvement: 0, poor: 0 },
          inp: { samples: 0, good: 0, needs_improvement: 0, poor: 0 },
        },
        breakdown: [],
      },
      response: new Response(null, { status: 200 }),
    })

    renderPage()

    expect(await screen.findByText(/full tracker script/i)).toBeVisible()
  })
})
