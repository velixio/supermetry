import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { renderWithRouter } from '@/test/renderWithRouter'

const { get, post, patch, del, copyText } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
  copyText: vi.fn(),
}))

vi.mock('@/lib/api', () => ({ api: { GET: get, POST: post, PATCH: patch, DELETE: del } }))
vi.mock('@/lib/clipboard', () => ({ copyText }))

import { PixelPage } from './PixelPage'
import type { TrackingPixel } from './types'

const pixel: TrackingPixel = {
  id: 11,
  site_id: 7,
  name: 'Spring newsletter',
  tag: 'spring-2026',
  gif_url: 'https://sm.example.test/px/abc.gif?c=spring-2026',
  svg_url: 'https://sm.example.test/px/abc.svg?c=spring-2026',
  html: '<img src="https://sm.example.test/px/abc.gif?c=spring-2026" width="1" height="1" alt="" style="display:none" />',
  total: 1234,
  views_30d: 81,
  created_at: '2026-10-01T00:00:00Z',
}

function renderPage(canManage = true) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return renderWithRouter(
    <QueryClientProvider client={client}>
      <PixelPage canManage={canManage} org="acme" siteId={7} siteSlug="main" />
    </QueryClientProvider>,
  )
}

describe('PixelPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    get.mockResolvedValue({ data: [pixel], response: { status: 200 } })
    copyText.mockResolvedValue(true)
  })
  afterEach(() => cleanup())

  it('lists managed pixels with their tag, totals and actions', async () => {
    renderPage()
    expect(await screen.findByRole('heading', { level: 2, name: 'Spring newsletter' })).toBeInTheDocument()
    expect(screen.getByText('spring-2026')).toBeInTheDocument()
    expect(screen.getByText('1,234')).toBeInTheDocument()
    expect(screen.getByText('81')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open Spring newsletter' })).toHaveAttribute(
      'href',
      '/app/acme/main/links/pixel/11',
    )
    fireEvent.click(screen.getByRole('button', { name: 'Copy image URL for Spring newsletter' }))
    await waitFor(() => expect(copyText).toHaveBeenCalledWith(pixel.gif_url))
    expect(get).toHaveBeenCalledWith('/api/private/sites/{site}/tracking-pixels', { params: { path: { site: 7 } } })
    expect(get.mock.calls.some(([path]) => path === '/api/private/sites/{site}/pixel/stats')).toBe(false)
  })

  it('shows an empty state', async () => {
    get.mockResolvedValue({ data: [], response: { status: 200 } })
    renderPage()
    expect(await screen.findByText('No tracking pixels yet.')).toBeInTheDocument()
  })

  it('creates a named pixel with an optional tag', async () => {
    post.mockResolvedValue({ data: pixel, response: { status: 201 } })
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Create pixel' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: '  Spring newsletter  ' } })
    fireEvent.change(within(dialog).getByLabelText('Tag'), { target: { value: ' spring-2026 ' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create pixel' }))
    await waitFor(() => expect(post).toHaveBeenCalledWith('/api/private/sites/{site}/tracking-pixels', {
      params: { path: { site: 7 } },
      body: { name: 'Spring newsletter', tag: 'spring-2026' },
    }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('reports load failures with a retry action', async () => {
    get.mockResolvedValueOnce({ error: { detail: 'boom' }, response: { status: 500 } })
    renderPage()
    expect(await screen.findByRole('alert')).toHaveTextContent('Tracking pixels could not be loaded.')
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('heading', { level: 2, name: 'Spring newsletter' })).toBeInTheDocument()
  })
})
