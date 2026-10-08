import '@testing-library/jest-dom/vitest'

import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

const { useRealtimePage } = vi.hoisted(() => ({ useRealtimePage: vi.fn() }))

vi.mock('./useRealtimePage', () => ({ useRealtimePage }))
vi.mock('@/components/Flag', () => ({
  Flag: ({ code }: { code: string }) => <span data-testid={`flag-${code}`} />,
}))

import { RealtimePage } from './RealtimePage'

beforeAll(() => {
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    unobserve() {}
    disconnect() {}
  })
})

const response = {
  active_visitors: 12,
  visitors_last_30m: 41,
  visitors_per_minute: Array.from({ length: 30 }, (_, index) => ({
    minute: `2026-10-08T12:${String(index).padStart(2, '0')}:00Z`,
    visitors: index + 1,
  })),
  pageviews_last_30m: 91,
  top_pages: [{ page: '/docs', visitors: 7 }],
  top_entry_pages: [{ page: '/pricing', visitors: 4 }],
  top_sources: [{ source: 'Google', visitors: 5 }],
  countries: [{ country_code: 'IN', visitors: 6 }],
  regions: [{ label: 'Maharashtra', country_code: 'IN', visitors: 4 }],
  cities: [{ label: 'Mumbai', country_code: 'IN', visitors: 3 }],
  devices: [{ label: 'Desktop', visitors: 8 }],
  browsers: [{ label: 'Chrome', visitors: 7 }],
  operating_systems: [{ label: 'Linux', visitors: 5 }],
  events: [{ name: 'Signup', count: 2 }],
  recent_activity: [
    {
      kind: 'custom', page: '/pricing', event_name: 'Signup', country_code: 'IN', city: 'Mumbai',
      device: 'Mobile', browser: 'Chrome', source: 'Google', timestamp: '2026-10-08T12:00:20Z',
    },
    {
      kind: 'pageview', page: '/docs', event_name: '', country_code: 'US', city: 'Austin',
      device: 'Desktop', browser: 'Firefox', source: 'Direct', timestamp: '2026-10-08T12:00:10Z',
    },
  ],
}

describe('RealtimePage', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-08T12:00:30Z'))
    useRealtimePage.mockReturnValue({ data: response, isPending: false, isError: false })
  })

  afterEach(() => {
    cleanup()
    vi.useRealTimers()
    vi.clearAllMocks()
  })

  it('renders equal-weight live and 30-minute headline counts with every ranked breakdown', () => {
    render(<RealtimePage dateRange={{ from: '2026-10-08', to: '2026-10-08' }} siteId={7} timezone="Asia/Kolkata" />)

    expect(screen.getByText('12')).toBeVisible()
    expect(screen.getByText('Live now')).toBeVisible()
    expect(screen.getByText('Active in the last 5 minutes')).toBeVisible()
    expect(screen.getByText('41')).toBeVisible()
    expect(screen.getByText('Last 30 minutes')).toBeVisible()
    expect(screen.getByText('Visitors')).toBeVisible()
    expect(screen.getByText('91 pageviews in the last 30 minutes')).toBeVisible()
    expect(screen.getByRole('img', { name: 'Visitors during the last 30 minutes' })).toBeVisible()
    expect(screen.getAllByText('/docs').length).toBeGreaterThan(0)
    expect(screen.getAllByText('/pricing').length).toBeGreaterThan(0)
    expect(screen.getAllByText('Signup').length).toBeGreaterThan(0)
    expect(screen.getAllByText('Google').length).toBeGreaterThan(0)
    expect(screen.getByText('India')).toBeVisible()
    expect(screen.getByText('Maharashtra')).toBeVisible()
    expect(screen.getAllByText('Mumbai').length).toBeGreaterThan(0)
    expect(screen.getAllByText('Desktop').length).toBeGreaterThan(0)

    fireEvent.mouseDown(screen.getByRole('tab', { name: 'Browsers' }), { button: 0, ctrlKey: false })
    expect(screen.getAllByText('Chrome').length).toBeGreaterThan(0)

    fireEvent.mouseDown(screen.getByRole('tab', { name: 'Operating systems' }), { button: 0, ctrlKey: false })
    expect(screen.getByText('Linux')).toBeVisible()
    expect(screen.getAllByTestId('flag-IN').length).toBeGreaterThan(0)
  })

  it('orders recent activity newest first and updates relative seconds', () => {
    render(<RealtimePage dateRange={{ from: '2026-10-08', to: '2026-10-08' }} siteId={7} timezone="UTC" />)

    const items = screen.getAllByRole('listitem', { name: /activity/i })
    expect(items[0]).toHaveTextContent('Signup')
    expect(items[1]).toHaveTextContent('/docs')
    expect(items[0]).toHaveTextContent('10s ago')

    act(() => vi.advanceTimersByTime(5_000))
    expect(items[0]).toHaveTextContent('15s ago')
  })

  it('renders explicit loading and error states', () => {
    useRealtimePage.mockReturnValueOnce({ data: undefined, isPending: true, isError: false })
    const { rerender } = render(<RealtimePage dateRange={{ from: '', to: '' }} siteId={7} timezone="UTC" />)
    expect(screen.getByLabelText('Loading realtime analytics')).toBeVisible()

    useRealtimePage.mockReturnValueOnce({ data: undefined, isPending: false, isError: true, error: { detail: 'offline' }, refetch: vi.fn() })
    rerender(<RealtimePage dateRange={{ from: '', to: '' }} siteId={7} timezone="UTC" />)
    expect(screen.getByText('offline')).toBeVisible()
  })
})
