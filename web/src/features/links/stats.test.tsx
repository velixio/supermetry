import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { renderWithRouter } from '@/test/renderWithRouter'

const { get, patch, del } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn(), del: vi.fn() }))
vi.mock('@/lib/api', () => ({ api: { GET: get, PATCH: patch, DELETE: del } }))
vi.mock('@/lib/clipboard', () => ({ copyText: vi.fn().mockResolvedValue(true) }))

import { daysBetween, DailyBars } from './DailyBars'
import { LinkStatsPage } from './LinkStatsPage'
import { PixelStatsSection } from './PixelStatsSection'
import { TrackingPixelPage } from './TrackingPixelPage'

function wrap(node: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return renderWithRouter(<QueryClientProvider client={client}>{node}</QueryClientProvider>)
}

describe('daily bars', () => {
  afterEach(() => cleanup())

  it('lists every calendar day inclusively', () => {
    expect(daysBetween('2026-02-27', '2026-03-02')).toEqual(['2026-02-27', '2026-02-28', '2026-03-01', '2026-03-02'])
    expect(daysBetween('2026-03-02', '2026-03-01')).toEqual([])
  })

  it('summarises the chart in text and exposes a data table', () => {
    render(<DailyBars daily={[{ day: '2026-10-10', count: 3 }, { day: '2026-10-12', count: 9 }]} from="2026-10-10" title="Clicks per day" to="2026-10-12" unit="Clicks" />)
    expect(screen.getByRole('img', { name: '12 clicks from 2026-10-10 to 2026-10-12. Busiest day 2026-10-12 with 9.' })).toBeInTheDocument()
    expect(screen.getByRole('table', { name: 'Clicks per day' })).toHaveTextContent('2026-10-12')
  })

  it('says so when there is nothing to chart', () => {
    render(<DailyBars daily={[]} from="2026-10-10" title="Clicks per day" to="2026-10-12" unit="Clicks" />)
    expect(screen.getByText('No clicks in this period.')).toBeInTheDocument()
    expect(screen.queryByRole('img')).not.toBeInTheDocument()
  })
})

describe('LinkStatsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    get.mockImplementation(async (path: string) => {
      if (path === '/api/private/links/{link}') {
        return { data: { id: 5, name: 'Spring sale', status: 'active', slug: 'spring', short_url: 'https://sm.example.test/l/spring', short_path: '/l/spring', destination: 'https://shop.example', clicks: 99 }, response: { status: 200 } }
      }
      return {
        data: {
          from: '2026-10-01', to: '2026-10-12', total: 7, daily: [{ day: '2026-10-11', count: 7 }],
          countries: [{ value: 'NP', clicks: 5 }, { value: '', clicks: 2 }],
          referrers: [{ value: 'news.example', clicks: 4 }, { value: '', clicks: 3 }],
          devices: [{ value: 'mobile', clicks: 7 }],
        },
        response: { status: 200 },
      }
    })
  })
  afterEach(() => cleanup())

  it('shows totals, breakdowns and refetches when the period changes', async () => {
    wrap(<LinkStatsPage linkId={5} org="acme" siteId={7} siteSlug="main" timezone="Asia/Kathmandu" />)
    expect(await screen.findByRole('heading', { level: 1, name: /Spring sale/ })).toBeInTheDocument()
    expect(await screen.findByText('news.example')).toBeInTheDocument()
    expect(screen.getByText('Direct or unknown')).toBeInTheDocument()
    expect(screen.getByText('Unknown')).toBeInTheDocument()
    expect(screen.getByText('Mobile')).toBeInTheDocument()
    expect(screen.getByText('99')).toBeInTheDocument()
    const before = get.mock.calls.filter(([path]) => path === '/api/private/links/{link}/stats').length
    fireEvent.change(screen.getByLabelText('Period'), { target: { value: '7d' } })
    await waitFor(() => expect(get.mock.calls.filter(([path]) => path === '/api/private/links/{link}/stats').length).toBe(before + 1))
  })
})

describe('TrackingPixelPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    get.mockImplementation(async (path: string) => {
      if (path === '/api/private/tracking-pixels/{id}') {
        return {
          data: {
            id: 11, site_id: 7, name: 'Spring newsletter', tag: 'spring', total: 6, views_30d: 6,
            gif_url: 'https://sm.example.test/px/abc.gif?c=spring',
            svg_url: 'https://sm.example.test/px/abc.svg?c=spring',
            html: '<img src="https://sm.example.test/px/abc.gif?c=spring" width="1" height="1" alt="" style="display:none" />',
            created_at: '2026-10-01T00:00:00Z',
          },
          response: { status: 200 },
        }
      }
      return { data: { from: '2026-10-01', to: '2026-10-12', summary: { today: 1, last_7: 4, last_30: 6, total: 6 }, total: 6, daily: [{ day: '2026-10-11', count: 6 }], sources: [{ source: 'other', impressions: 4 }, { source: 'github', impressions: 2 }], countries: [{ country: 'NP', impressions: 3 }], cities: [{ country: 'NP', city: 'Kathmandu', impressions: 3 }] }, response: { status: 200 } }
    })
  })
  afterEach(() => cleanup())

  it('opens on campaign-scoped stats without the install snippets', async () => {
    wrap(<TrackingPixelPage canManage org="acme" pixelId={11} siteId={7} siteSlug="main" timezone="UTC" />)
    expect(await screen.findByRole('heading', { name: 'Spring newsletter' })).toBeInTheDocument()
    expect(screen.getByText('spring')).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Stats' })).toHaveAttribute('aria-selected', 'true')
    await waitFor(() => {
      const calls = get.mock.calls.filter(([path]) => path === '/api/private/sites/{site}/pixel/stats')
      expect(calls.length).toBeGreaterThan(0)
      expect(calls.every(([, options]) => options.params.query.campaign === 'spring')).toBe(true)
    })
    expect(await screen.findByRole('heading', { name: 'Where views come from' })).toBeInTheDocument()
    expect(screen.getByText('GitHub')).toBeInTheDocument()
    expect(screen.getByText('Kathmandu, Nepal')).toBeInTheDocument()
    expect(screen.queryByText('https://sm.example.test/px/abc.gif?c=spring')).not.toBeInTheDocument()
    expect(screen.queryByRole('img', { name: 'Badge with the total' })).not.toBeInTheDocument()
  })

  it('shows the snippets and badge previews on the install tab', async () => {
    wrap(<TrackingPixelPage canManage org="acme" pixelId={11} siteId={7} siteSlug="main" timezone="UTC" />)
    fireEvent.mouseDown(await screen.findByRole('tab', { name: 'Install' }), { button: 0 })
    expect(await screen.findByText('https://sm.example.test/px/abc.gif?c=spring')).toBeInTheDocument()
    expect(screen.getByRole('img', { name: 'Badge with the total' })).toHaveAttribute('src', 'https://sm.example.test/px/abc.svg?c=spring&preview=1')
    expect(screen.getByRole('img', { name: 'Badge with total, today and a 30-day graph' })).toHaveAttribute('src', 'https://sm.example.test/px/abc.svg?c=spring&style=stats&preview=1')
    expect(screen.queryByRole('heading', { name: 'Where views come from' })).not.toBeInTheDocument()
  })

  it('asks for confirmation before deleting', async () => {
    del.mockResolvedValue({ response: { status: 204 } })
    wrap(<TrackingPixelPage canManage org="acme" pixelId={11} siteId={7} siteSlug="main" timezone="UTC" />)
    fireEvent.click(await screen.findByRole('button', { name: 'Delete pixel' }))
    expect(del).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Delete tracking pixel' }))
    await waitFor(() => expect(del).toHaveBeenCalledWith('/api/private/tracking-pixels/{id}', { params: { path: { id: 11 } } }))
  })

  it('renames the pixel without sending its immutable tag', async () => {
    patch.mockResolvedValue({
      data: {
        id: 11, site_id: 7, name: 'Renamed pixel', tag: 'spring', total: 6, views_30d: 6,
        gif_url: 'https://sm.example.test/px/abc.gif?c=spring',
        svg_url: 'https://sm.example.test/px/abc.svg?c=spring',
        html: '', created_at: '2026-10-01T00:00:00Z',
      },
      response: { status: 200 },
    })
    wrap(<TrackingPixelPage canManage org="acme" pixelId={11} siteId={7} siteSlug="main" timezone="UTC" />)
    fireEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: ' Renamed pixel ' } })
    expect(within(dialog).queryByLabelText('Tag')).not.toBeInTheDocument()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save name' }))
    await waitFor(() => expect(patch).toHaveBeenCalledWith('/api/private/tracking-pixels/{id}', {
      params: { path: { id: 11 } },
      body: { name: 'Renamed pixel' },
    }))
  })
})

describe('PixelStatsSection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    get.mockImplementation(async (_path: string, options: { params: { query: { from: string; to: string } } }) => ({
      data: {
        from: options.params.query.from, to: options.params.query.to,
        summary: { today: 3, last_7: 3, last_30: 3, total: 3 }, total: 3,
        daily: [{ day: options.params.query.to, count: 3 }],
        hourly: [{ hour: `${options.params.query.to}T09:00`, count: 2 }, { hour: `${options.params.query.to}T14:00`, count: 1 }],
        sources: [], countries: [], cities: [],
      },
      response: { status: 200 },
    }))
  })
  afterEach(() => cleanup())

  it('offers hourly grouping only for ranges of seven days or less', async () => {
    wrap(<PixelStatsSection siteId={7} tag="spring" timezone="UTC" />)
    const group = await screen.findByLabelText('Group by')
    expect(within(group).getByRole('option', { name: 'By hour' })).toBeDisabled()
    fireEvent.change(screen.getByLabelText('Period'), { target: { value: '7d' } })
    expect(within(group).getByRole('option', { name: 'By hour' })).toBeEnabled()
  })

  it('switches to hours for today and charts the hourly counts', async () => {
    wrap(<PixelStatsSection siteId={7} tag="spring" timezone="UTC" />)
    fireEvent.change(await screen.findByLabelText('Period'), { target: { value: 'today' } })
    expect(screen.getByLabelText('Group by')).toHaveValue('hour')
    const table = await screen.findByRole('table')
    expect(within(table).getByRole('columnheader', { name: 'Hour' })).toBeInTheDocument()
    const rows = within(table).getAllByRole('row').slice(1).map((row) => row.textContent)
    expect(rows).toHaveLength(2)
    expect(rows[0]).toMatch(/09:00.*2$/)
    expect(rows[1]).toMatch(/14:00.*1$/)
    expect(screen.getByRole('img', { name: /Busiest hour .*09:00 with 2/ })).toBeInTheDocument()
  })

  it('falls back to days when a longer period is chosen', async () => {
    wrap(<PixelStatsSection siteId={7} tag="spring" timezone="UTC" />)
    fireEvent.change(await screen.findByLabelText('Period'), { target: { value: 'yesterday' } })
    expect(screen.getByLabelText('Group by')).toHaveValue('hour')
    fireEvent.change(screen.getByLabelText('Period'), { target: { value: '30d' } })
    expect(screen.getByLabelText('Group by')).toHaveValue('day')
  })
})
