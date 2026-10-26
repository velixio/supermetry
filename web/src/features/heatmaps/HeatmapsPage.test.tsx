import '@testing-library/jest-dom/vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { useHeatmapReport, useHeatmapSite, useStats } = vi.hoisted(() => ({
  useHeatmapReport: vi.fn(),
  useHeatmapSite: vi.fn(),
  useStats: vi.fn(),
}))

vi.mock('./api', () => ({ useHeatmapReport, useHeatmapSite }))
vi.mock('@/lib/stats', () => ({ useStats }))

import { HeatmapsPage } from './HeatmapsPage'

const report = {
  cells: [
    { gx: 25, gy: 10, clicks: 3 },
    { gx: 75, gy: 40, clicks: 9 },
  ],
  scroll_bands: [
    { band: 10, share: 1 },
    { band: 50, share: 0.64 },
    { band: 90, share: 0.18 },
  ],
  totals: { pageviews_sampled: 42, clicks: 12 },
}

function renderPage(overrides: Partial<React.ComponentProps<typeof HeatmapsPage>> = {}) {
  const props: React.ComponentProps<typeof HeatmapsPage> = {
    siteId: 7,
    orgId: 3,
    dateRange: { from: '2026-10-01', to: '2026-10-08' },
    device: 'mobile',
    path: '/pricing',
    onChange: vi.fn(),
    ...overrides,
  }
  return { ...render(<HeatmapsPage {...props} />), props }
}

describe('HeatmapsPage', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    useHeatmapSite.mockReturnValue({
      data: { allowed_hosts: ['shop.supermetry.com'], heatmaps_enabled: true },
      isPending: false,
      isError: false,
    })
    useStats.mockReturnValue({
      data: { rows: [{ page: '/pricing' }, { page: '/docs' }] },
      isPending: false,
      isError: false,
    })
    useHeatmapReport.mockReturnValue({
      data: report,
      isPending: false,
      isLoading: false,
      isError: false,
    })
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
      clearRect: vi.fn(),
      setTransform: vi.fn(),
      createRadialGradient: vi.fn(() => ({ addColorStop: vi.fn() })),
      beginPath: vi.fn(),
      arc: vi.fn(),
      fill: vi.fn(),
      set fillStyle(_value: string | CanvasGradient | CanvasPattern) {},
      set globalAlpha(_value: number) {},
    } as unknown as CanvasRenderingContext2D)
  })

  afterEach(() => {
    cleanup()
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('renders the selected live page in a sandboxed device preview', () => {
    const { props } = renderPage()

    expect(screen.queryByRole('main')).not.toBeInTheDocument()
    const frame = screen.getByTitle('Live preview of /pricing')
    expect(frame).toHaveAttribute('sandbox', 'allow-same-origin allow-scripts')
    expect(frame).toHaveAttribute('src', 'https://shop.supermetry.com/pricing')
    expect(frame).toHaveStyle({ width: '390px' })

    fireEvent.change(screen.getByLabelText('Page'), { target: { value: '/docs' } })
    expect(props.onChange).toHaveBeenCalledWith({ device: 'mobile', path: '/docs' })
  })

  it('preserves a URL-selected page that is outside the current top pages', () => {
    renderPage({ path: '/campaign' })

    expect(screen.getByLabelText('Page')).toHaveValue('/campaign')
    expect(screen.getByTitle('Live preview of /campaign')).toHaveAttribute(
      'src',
      'https://shop.supermetry.com/campaign',
    )
  })

  it('switches device widths and draws click cells on a canvas', () => {
    const { props } = renderPage({ device: 'desktop' })

    expect(screen.getByTitle('Live preview of /pricing')).toHaveStyle({ width: '1280px' })
    const tablet = screen.getByRole('tab', { name: 'Tablet' })
    fireEvent.pointerDown(tablet, { button: 0, ctrlKey: false })
    fireEvent.click(tablet)
    expect(props.onChange).toHaveBeenCalledWith({ device: 'tablet', path: '/pricing' })
    expect(screen.getByLabelText('Click density overlay')).toBeVisible()
    expect(screen.getByText(/pageviews sampled/)).toHaveTextContent('12 clicks · 42 pageviews sampled')
  })

  it('caps the canvas bitmap while preserving a tall page overlay', () => {
    useHeatmapReport.mockReturnValue({
      data: {
        ...report,
        cells: [{ gx: 50, gy: 4999, clicks: 1 }],
      },
      isPending: false,
      isLoading: false,
      isError: false,
    })

    renderPage({ device: 'desktop' })

    const canvas = screen.getByLabelText('Click density overlay')
    expect(canvas).toHaveAttribute('width', '640')
    expect(canvas).toHaveAttribute('height', '8192')
    expect(canvas.parentElement).toHaveStyle({ height: '100000px' })
  })

  it('shows scroll reach bands and allows hiding them', () => {
    renderPage()

    fireEvent.click(screen.getByRole('button', { name: 'Scroll map' }))
    expect(screen.getByText('50% depth · 64% reached')).toBeVisible()
    expect(screen.getByText('90% depth · 18% reached')).toBeVisible()

    fireEvent.click(screen.getByRole('button', { name: 'Scroll map' }))
    expect(screen.queryByText('50% depth · 64% reached')).not.toBeInTheDocument()
  })

  it('falls back to a page-height grid when the iframe does not load', () => {
    renderPage()

    act(() => vi.advanceTimersByTime(4_000))

    expect(screen.getByText('Page preview blocked')).toBeVisible()
    expect(screen.getByText(/frame-ancestors/i)).toBeVisible()
    expect(screen.getByLabelText('Page outline')).toHaveStyle({ height: '820px' })
  })

  it('keeps a successfully loaded iframe after the fallback deadline', () => {
    renderPage()
    const frame = screen.getByTitle('Live preview of /pricing')

    fireEvent.load(frame)
    act(() => vi.advanceTimersByTime(4_000))

    expect(screen.getByTitle('Live preview of /pricing')).toBeVisible()
    expect(screen.queryByText('Page preview blocked')).not.toBeInTheDocument()
  })

  it('explains the required setup when heatmaps are disabled', () => {
    useHeatmapSite.mockReturnValue({
      data: { allowed_hosts: ['shop.supermetry.com'], heatmaps_enabled: false },
      isPending: false,
      isError: false,
    })

    renderPage()

    expect(screen.getByText('Heatmaps are not collecting yet')).toBeVisible()
    expect(screen.getByText(/full tracker script/i)).toBeVisible()
    expect(screen.queryByTitle('Live preview of /pricing')).not.toBeInTheDocument()
  })

  it('does not mistake a failed site settings request for a disabled heatmap', () => {
    const refetch = vi.fn()
    useHeatmapSite.mockReturnValue({
      error: { detail: 'Site settings could not be loaded.' },
      isPending: false,
      isError: true,
      refetch,
    })

    renderPage()

    expect(screen.getByRole('alert')).toHaveTextContent('Site settings could not be loaded.')
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(refetch).toHaveBeenCalledOnce()
    expect(screen.queryByText('Heatmaps are not collecting yet')).not.toBeInTheDocument()
  })

  it('shows a retryable error when the heatmap query fails', () => {
    const refetch = vi.fn()
    useHeatmapReport.mockReturnValue({
      error: { detail: 'The heatmap query timed out.' },
      isPending: false,
      isLoading: false,
      isError: true,
      refetch,
    })

    renderPage()

    expect(screen.getByRole('alert')).toHaveTextContent('The heatmap query timed out.')
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(refetch).toHaveBeenCalledOnce()
  })

  it('shows an empty state when zero-value scroll bands contain no samples', () => {
    useHeatmapReport.mockReturnValue({
      data: {
        cells: [],
        scroll_bands: Array.from({ length: 10 }, (_, index) => ({ band: (index + 1) * 10, share: 0 })),
        totals: { pageviews_sampled: 0, clicks: 0 },
      },
      isPending: false,
      isLoading: false,
      isError: false,
    })

    renderPage()

    expect(screen.getByText('No heatmap data for this selection')).toBeVisible()
    expect(screen.queryByTitle('Live preview of /pricing')).not.toBeInTheDocument()
  })
})
