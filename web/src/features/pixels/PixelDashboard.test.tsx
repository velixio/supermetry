import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { PixelDashboard } from './PixelDashboard'

afterEach(() => { cleanup(); vi.unstubAllGlobals() })

const pixel = { id: 1, public_id: 'pAAAAAAAAAAA', name: 'GitHub profile', badge_label: 'views', enabled: true, created_at: '2026-10-01T00:00:00Z', total: 1234, views_30d: 56, gif_url: 'https://sm.example/px/pAAAAAAAAAAA.gif', svg_url: 'https://sm.example/px/pAAAAAAAAAAA.svg' }
const stats = { from: '2026-09-15', to: '2026-10-14', summary: { today: 7, last_7: 21, last_30: 56, total: 1234 }, daily: [{ day: '2026-10-14', proxy: 50, direct: 6 }], pixels: [{ pixel_id: 1, views: 56 }], countries: [{ country: 'NP', views: 6 }] }

describe('PixelDashboard', () => {
  it('shows views, the pixel list with snippets, and the country note', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = input instanceof Request ? input.url : String(input)
      return new Response(JSON.stringify(url.includes('/stats') ? stats : [pixel]), { headers: { 'Content-Type': 'application/json' } })
    }))
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <PixelDashboard site={{ id: 7, name: 'README', kind: 'pixel', timezone: 'UTC' }} homepage="https://supermetry.com" />
      </QueryClientProvider>,
    )
    expect(await screen.findByText('GitHub profile')).toBeInTheDocument()
    expect(screen.getAllByText('1.2k')).toHaveLength(2)
    expect(screen.getByText('Today')).toBeInTheDocument()
    expect(screen.getAllByText('Last 7 days').length).toBeGreaterThan(0)
    expect(screen.getByText('21')).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Group by' })).toBeInTheDocument()
    expect(screen.getByText(/views, not unique visitors/)).toBeInTheDocument()
    expect(screen.getByText(/hide the country/)).toBeInTheDocument()
    expect(screen.getByRole('img', { name: /GitHub profile badge preview/ })).toHaveAttribute('src', `${pixel.svg_url}?preview=1`)
    expect(screen.getByRole('button', { name: /Copy linked Markdown badge/ })).toBeInTheDocument()
  })
})
