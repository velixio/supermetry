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
    fcp: { p75: 1900, samples: 16, good: 0.5, needs_improvement: 0.3, poor: 0.2 },
    ttfb: { p75: 1900, samples: 14, good: 0.4, needs_improvement: 0.3, poor: 0.3 },
  },
  breakdown: [
    {
      value: '/pricing',
      lcp: { p75: 3100, samples: 10, good: 0.5, needs_improvement: 0.3, poor: 0.2 },
      cls: { p75: 120, samples: 9, good: 0.7, needs_improvement: 0.2, poor: 0.1 },
      inp: { p75: 220, samples: 7, good: 0.4, needs_improvement: 0.4, poor: 0.2 },
      fcp: { p75: 1800, samples: 8, good: 0.6, needs_improvement: 0.3, poor: 0.1 },
      ttfb: { p75: 900, samples: 6, good: 0.5, needs_improvement: 0.3, poor: 0.2 },
    },
  ],
  timeseries: [
    { date: '2026-09-29', lcp: { p50: 2100, p75: 2800, p95: 4200, samples: 8 }, cls: { p50: 50, p75: 80, p95: 160, samples: 7 }, inp: { p50: 180, p75: 240, p95: 550, samples: 5 }, fcp: { p50: 1400, p75: 1900, p95: 3200, samples: 7 }, ttfb: { p50: 600, p75: 900, p95: 1900, samples: 6 } },
    { date: '2026-09-30', lcp: { p50: 1900, p75: 2400, p95: 3900, samples: 12 }, cls: { p50: 40, p75: 70, p95: 140, samples: 11 }, inp: { p50: 160, p75: 200, p95: 480, samples: 7 }, fcp: { p50: 1300, p75: 1700, p95: 2800, samples: 9 }, ttfb: { p50: 500, p75: 700, p95: 1600, samples: 8 } },
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
    expect(screen.getByRole('region', { name: 'First Contentful Paint' })).toHaveTextContent('1.9 s')
    expect(screen.getByRole('region', { name: 'First Contentful Paint' })).toHaveTextContent('Needs improvement')
    expect(screen.getByRole('region', { name: 'Time to First Byte' })).toHaveTextContent('1.9 s')
    expect(screen.getByRole('region', { name: 'Time to First Byte' })).toHaveTextContent('Poor')

    const distribution = within(
      screen.getByRole('region', { name: 'Largest Contentful Paint' }),
    ).getByRole('img', { name: '60% good, 25% needs improvement, 15% poor' })
    expect(distribution).toBeVisible()

    await waitFor(() => expect(post).toHaveBeenCalled())
    expect(post.mock.calls[0][0]).toBe('/api/private/vitals/query')
    expect(post.mock.calls[0][1]).toEqual(expect.objectContaining({
        body: {
          site_id: 7,
          date_range: { from: '2026-09-01', to: '2026-09-30' },
          filters: [{ dimension: 'country', operator: 'is', values: ['IN'] }],
          dimension: 'page',
        },
        signal: expect.any(AbortSignal),
      }))
  })

  it('switches trend metrics while retaining percentile and sample context', async () => {
    renderPage()

    const trend = await screen.findByRole('region', { name: 'Web Vitals trend' })
    expect(within(trend).getByRole('img', { name: 'Largest Contentful Paint daily percentiles' })).toBeVisible()
    expect(within(trend).getByText('20 samples shown')).toBeVisible()
    fireEvent.mouseDown(within(trend).getByRole('tab', { name: 'CLS' }), { button: 0 })
    fireEvent.click(within(trend).getByRole('tab', { name: 'CLS' }))
    expect(within(trend).getByRole('img', { name: 'Cumulative Layout Shift daily percentiles' })).toBeVisible()
    expect(within(trend).getByText('18 samples shown')).toBeVisible()
    const table = within(trend).getByRole('table', { name: 'Cumulative Layout Shift daily trend data' })
    expect(table).toHaveTextContent('0.080')
  })

  it.each([
    ['Device', 'device'],
    ['Country', 'country'],
    ['Browser', 'browser'],
  ] as const)('switches the breakdown to %s', async (label, dimension) => {
    renderPage()
    expect(await screen.findByRole('cell', { name: '/pricing' })).toBeVisible()

    const tab = screen.getByRole('tab', { name: label })
    fireEvent.mouseDown(tab, { button: 0 })
    fireEvent.click(tab)

    await waitFor(() =>
      expect(post).toHaveBeenLastCalledWith('/api/private/vitals/query', expect.objectContaining({
        body: expect.objectContaining({ dimension }),
      })),
    )
    expect(await screen.findByRole('columnheader', { name: label })).toBeVisible()
  })

  it('renders country names instead of raw country codes', async () => {
    post.mockResolvedValue({
      data: { ...response, breakdown: [{ ...response.breakdown[0], value: 'US' }] },
      response: new Response(null, { status: 200 }),
    })
    renderPage()

    const country = await screen.findByRole('tab', { name: 'Country' })
    fireEvent.mouseDown(country, { button: 0 })
    fireEvent.click(country)

    expect(await screen.findByRole('cell', { name: 'United States' })).toBeVisible()
    expect(screen.queryByRole('cell', { name: 'US' })).not.toBeInTheDocument()
  })

  it('explains that empty reports need the full tracker', async () => {
    post.mockResolvedValue({
      data: {
        overall: {
          lcp: { samples: 0, good: 0, needs_improvement: 0, poor: 0 },
          cls: { samples: 0, good: 0, needs_improvement: 0, poor: 0 },
          inp: { samples: 0, good: 0, needs_improvement: 0, poor: 0 },
          fcp: { samples: 0, good: 0, needs_improvement: 0, poor: 0 },
          ttfb: { samples: 0, good: 0, needs_improvement: 0, poor: 0 },
        },
        breakdown: [],
        timeseries: [],
      },
      response: new Response(null, { status: 200 }),
    })

    renderPage()

    expect(await screen.findByText(/full tracker script/i)).toBeVisible()
  })

  it('shows trend loading and request errors', async () => {
    post.mockImplementation(() => new Promise(() => {}))
    renderPage()
    expect(screen.getByLabelText('Loading Web Vitals')).toBeVisible()
    cleanup()

    post.mockResolvedValue({ error: { detail: "Web Vitals are available only within the site's 30-day raw retention window" }, response: new Response(null, { status: 400 }) })
    renderPage()
    expect(await screen.findByRole('alert')).toHaveTextContent('30-day raw retention window')
  })
})
