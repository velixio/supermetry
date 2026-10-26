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

import { LinksPage } from './LinksPage'
import type { ShortLink } from './types'

const base: ShortLink = {
  id: 1, site_id: 7, slug: 'spring', name: 'Spring sale', destination: 'https://shop.example/offer',
  utm_source: 'mail', utm_medium: '', utm_campaign: 'spring', utm_term: '', utm_content: '',
  enabled: true, expires_at: null, click_limit: null, clicks: 1234, status: 'active',
  short_path: '/l/spring', short_url: 'https://sm.example.test/l/spring',
  created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z',
}
const expired: ShortLink = { ...base, id: 2, slug: 'old', name: 'Old promo', status: 'expired', clicks: 3, short_url: 'https://sm.example.test/l/old' }

function renderPage(canManage = true) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return renderWithRouter(
    <QueryClientProvider client={client}>
      <LinksPage canManage={canManage} org="acme" siteId={7} siteSlug="main" />
    </QueryClientProvider>,
  )
}

describe('LinksPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    get.mockResolvedValue({ data: [base, expired], response: { status: 200 } })
    copyText.mockResolvedValue(true)
  })
  afterEach(() => cleanup())

  it('lists links with status, destination and click counts under one heading', async () => {
    renderPage()
    expect(await screen.findByRole('heading', { level: 2, name: /Spring sale/ })).toBeInTheDocument()
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1)
    expect(screen.getByText('https://sm.example.test/l/spring')).toBeInTheDocument()
    expect(screen.getByText('1,234')).toBeInTheDocument()
    expect(screen.getByText('Active')).toBeInTheDocument()
    expect(screen.getByText('Expired')).toBeInTheDocument()
    expect(get).toHaveBeenCalledWith('/api/private/sites/{site}/links', { params: { path: { site: 7 } } })
  })

  it('hides management controls from viewers but keeps statistics and copy', async () => {
    renderPage(false)
    await screen.findByRole('heading', { level: 2, name: /Spring sale/ })
    expect(screen.queryByRole('button', { name: /Create link/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Edit/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Delete/ })).not.toBeInTheDocument()
    expect(screen.getAllByRole('link', { name: /^Statistics/ }).length).toBeGreaterThan(0)
    expect(screen.getByRole('button', { name: 'Copy short URL for Spring sale' })).toBeInTheDocument()
  })

  it('shows an empty state', async () => {
    get.mockResolvedValue({ data: [], response: { status: 200 } })
    renderPage()
    expect(await screen.findByText('No short links yet.')).toBeInTheDocument()
  })

  it('reports load failures with a retry action', async () => {
    get.mockResolvedValueOnce({ error: { detail: 'boom' }, response: { status: 500 } })
    renderPage()
    expect(await screen.findByRole('alert')).toHaveTextContent('Links could not be loaded.')
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('heading', { level: 2, name: /Spring sale/ })).toBeInTheDocument()
  })

  it('copies the absolute short URL and announces it', async () => {
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Copy short URL for Spring sale' }))
    await waitFor(() => expect(copyText).toHaveBeenCalledWith('https://sm.example.test/l/spring'))
    expect((await screen.findAllByRole('status')).some((node) => node.textContent === 'Copied')).toBe(true)
  })

  it('creates a link from the dialog, sending trimmed values and a UTC expiry', async () => {
    post.mockResolvedValue({ data: { ...base, id: 3 }, response: { status: 201 } })
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: /Create link/ }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: '  Launch  ' } })
    fireEvent.change(within(dialog).getByLabelText('Destination URL'), { target: { value: ' https://supermetry.com/launch ' } })
    fireEvent.change(within(dialog).getByLabelText('Short URL ending'), { target: { value: 'launch' } })
    fireEvent.change(within(dialog).getByLabelText('Campaign'), { target: { value: 'oct' } })
    fireEvent.change(within(dialog).getByLabelText('Click limit'), { target: { value: '50' } })
    fireEvent.change(within(dialog).getByLabelText('Expires'), { target: { value: '2030-01-02T03:04' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save link' }))
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    const [path, options] = post.mock.calls[0]!
    expect(path).toBe('/api/private/sites/{site}/links')
    expect(options.params).toEqual({ path: { site: 7 } })
    expect(options.body).toMatchObject({
      name: 'Launch', destination: 'https://supermetry.com/launch', slug: 'launch', utm_campaign: 'oct', enabled: true, click_limit: 50,
    })
    expect(options.body.expires_at).toBe(new Date('2030-01-02T03:04').toISOString())
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('edits with a patch that clears an expiry and shows server validation errors', async () => {
    patch.mockResolvedValueOnce({ error: { detail: 'destination must start with http:// or https://' }, response: { status: 400 } })
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Edit Spring sale' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByLabelText('Name')).toHaveValue('Spring sale')
    fireEvent.change(within(dialog).getByLabelText('Short URL ending'), { target: { value: 'spring2' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save link' }))
    expect(await within(dialog).findByText(/must start with http/)).toBeInTheDocument()
    const [path, options] = patch.mock.calls[0]!
    expect(path).toBe('/api/private/links/{link}')
    expect(options.params).toEqual({ path: { link: 1 } })
    expect(options.body).toMatchObject({ slug: 'spring2', expires_at: null, click_limit: null })
  })

  it('asks for confirmation before deleting', async () => {
    del.mockResolvedValue({ response: { status: 204 } })
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Delete Old promo' }))
    expect(del).not.toHaveBeenCalled()
    fireEvent.click(await screen.findByRole('button', { name: 'Delete link' }))
    await waitFor(() => expect(del).toHaveBeenCalledWith('/api/private/links/{link}', { params: { path: { link: 2 } } }))
  })

  it('renders a QR code locally for the short URL', async () => {
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'QR code Spring sale' }))
    const image = await screen.findByRole('img', { name: 'QR code that opens https://sm.example.test/l/spring' })
    expect(image.querySelector('path')?.getAttribute('d')?.length).toBeGreaterThan(100)
    expect(get).not.toHaveBeenCalledWith(expect.stringContaining('qr'), expect.anything())
  })
})
