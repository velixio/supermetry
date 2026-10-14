import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { renderWithRouter } from '@/test/renderWithRouter'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/lib/api', () => ({ api: { GET: get } }))
vi.mock('@/lib/clipboard', () => ({ copyText: vi.fn().mockResolvedValue(true) }))

import { daysBetween, DailyBars } from './DailyBars'
import { LinkStatsPage } from './LinkStatsPage'
import { CampaignPage } from './CampaignPage'
import { PixelPage } from './PixelPage'

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

describe('PixelPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    get.mockImplementation(async (path: string, options: { params: { query?: { campaign?: string } } }) => {
      if (path === '/api/private/sites/{site}/pixel') {
        const campaign = options.params.query?.campaign ?? ''
        const url = `https://sm.example.test/px/abc.gif${campaign ? `?c=${campaign}` : ''}`
        return { data: { campaign, path: '/px/abc.gif', url, html: `<img src="${url}" width="1" height="1" alt="" />` }, response: { status: 200 } }
      }
      return { data: { from: '2026-10-01', to: '2026-10-12', summary: { today: 3, last_7: 5, last_30: 8, total: 8 }, total: 8, daily: [{ day: '2026-10-11', count: 8 }], campaigns: [{ campaign: 'spring', impressions: 6 }, { campaign: '', impressions: 2 }] }, response: { status: 200 } }
    })
  })
  afterEach(() => cleanup())

  it('builds the snippet for a campaign and ignores invalid tags', async () => {
    wrap(<PixelPage org="acme" siteId={7} siteSlug="main" timezone="UTC" />)
    expect(await screen.findByText(/px\/abc\.gif"/)).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Campaign tag'), { target: { value: 'spring-2026' } })
    expect(await screen.findByText(/abc\.gif\?c=spring-2026/, { selector: 'code' })).toBeInTheDocument()
    const calls = get.mock.calls.filter(([path]) => path === '/api/private/sites/{site}/pixel').length
    fireEvent.change(screen.getByLabelText('Campaign tag'), { target: { value: '<script>' } })
    expect(screen.getByLabelText('Campaign tag')).toHaveAttribute('aria-invalid', 'true')
    expect(get.mock.calls.filter(([path]) => path === '/api/private/sites/{site}/pixel').length).toBe(calls)
    expect(await screen.findByText('Untagged')).toBeInTheDocument()
    expect(screen.getAllByText('8').length).toBeGreaterThan(0)
  })

  it('links each campaign to its own page', async () => {
    wrap(<PixelPage org="acme" siteId={7} siteSlug="main" timezone="UTC" />)
    expect(await screen.findByRole('link', { name: /^spring/ })).toHaveAttribute('href', '/app/acme/main/links/pixel/spring')
    expect(screen.getByRole('link', { name: /^Untagged/ })).toHaveAttribute('href', '/app/acme/main/links/pixel/~untagged')
  })
})

describe('CampaignPage', () => {
  afterEach(() => cleanup())

  it('shows the stats of one tag with its image URL', async () => {
    get.mockImplementation(async (path: string, options: { params: { query?: { campaign?: string } } }) => {
      if (path === '/api/private/sites/{site}/pixel') {
        return { data: { campaign: 'spring', path: '/px/abc.gif', url: 'https://sm.example.test/px/abc.gif?c=spring', html: '' }, response: { status: 200 } }
      }
      return { data: { from: '2026-10-01', to: '2026-10-12', summary: { today: 1, last_7: 4, last_30: 6, total: 6 }, total: 6, daily: [{ day: '2026-10-11', count: 6 }], campaigns: [{ campaign: 'spring', impressions: 6 }] }, response: { status: 200 } }
    })
    wrap(<CampaignPage org="acme" segment="spring" siteId={7} siteSlug="main" timezone="UTC" />)
    expect(await screen.findByRole('heading', { name: 'Campaign: spring' })).toBeInTheDocument()
    expect(await screen.findByText('https://sm.example.test/px/abc.gif?c=spring')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Campaigns' })).not.toBeInTheDocument()
    await waitFor(() => expect(get.mock.calls.some(([path, options]) => path === '/api/private/sites/{site}/pixel/stats' && options.params.query.campaign === 'spring')).toBe(true))
  })

  it('maps the untagged segment to the untagged filter', async () => {
    get.mockImplementation(async () => ({ data: { from: '2026-10-01', to: '2026-10-12', summary: { today: 0, last_7: 0, last_30: 0, total: 0 }, total: 0, daily: [], campaigns: [] }, response: { status: 200 } }))
    wrap(<CampaignPage org="acme" segment="~untagged" siteId={7} siteSlug="main" timezone="UTC" />)
    expect(await screen.findByRole('heading', { name: 'Campaign: Untagged' })).toBeInTheDocument()
    await waitFor(() => expect(get.mock.calls.some(([path, options]) => path === '/api/private/sites/{site}/pixel/stats' && options.params.query.campaign === '(none)')).toBe(true))
    expect(get.mock.calls.some(([path]) => path === '/api/private/sites/{site}/pixel')).toBe(false)
  })
})
