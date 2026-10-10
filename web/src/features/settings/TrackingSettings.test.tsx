import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

const { get, patch } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn() }))

vi.mock('@/lib/api', () => ({ api: { GET: get, PATCH: patch } }))

import { TrackingSettings } from './TrackingSettings'

const site = {
  id: 7,
  public_id: 'abc123def456',
  name: 'Docs',
  timezone: 'UTC',
  tracker_url: 'https://supermetry.example/js/abc123def456.js',
  tracker: {
    outbound: true,
    downloads: true,
    forms: true,
    vitals: true,
    not_found: true,
    hash: false,
    clicks: false,
  },
  strict_origin: false,
  blocked_referrers: [],
  exclude_countries: [],
}

function renderSettings(role: 'owner' | 'admin' | 'editor' | 'viewer' = 'owner') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <TrackingSettings orgId={3} role={role} siteId={7} />
    </QueryClientProvider>,
  )
}

function toggle(control: HTMLElement) {
  fireEvent.pointerDown(control, { button: 0, ctrlKey: false })
  fireEvent.click(control)
}

describe('TrackingSettings', () => {
  beforeAll(() => {
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
      unobserve() {}
      disconnect() {}
    })
  })

  beforeEach(() => {
    vi.clearAllMocks()
    get.mockResolvedValue({ data: site, response: { ok: true, status: 200 } })
    patch.mockResolvedValue({
      data: { ...site, tracker: { ...site.tracker, hash: true } },
      response: { ok: true, status: 200 },
    })
  })

  afterEach(cleanup)

  it('shows all tracker controls, their guidance, and the per-site script URL', async () => {
    renderSettings()

    expect(await screen.findAllByRole('switch')).toHaveLength(8)
    expect(screen.getByRole('switch', { name: 'Require browser origin' })).not.toBeChecked()
    expect(screen.getByRole('textbox', { name: 'Blocked referrers' })).toBeVisible()
    expect(screen.getByText(/server-side senders and non-browser clients/i)).toBeVisible()
    expect(screen.getByText('Clicks follow Heatmaps unless changed')).toBeVisible()
    expect(screen.getByText('Turning every extension off serves the 1 KB core script, without custom-event tags')).toBeVisible()
    expect(screen.getByText(site.tracker_url)).toBeVisible()
    expect(screen.getByRole('button', { name: 'Copy script URL' })).toBeEnabled()
  })

  it('patches strict origin and custom blocked referrers', async () => {
    patch.mockResolvedValue({
      data: { ...site, strict_origin: true, blocked_referrers: ['evil.example', 'spam.example'] },
      response: { ok: true, status: 200 },
    })
    renderSettings()

    toggle(await screen.findByRole('switch', { name: 'Require browser origin' }))
    fireEvent.change(screen.getByRole('textbox', { name: 'Blocked referrers' }), {
      target: { value: 'evil.example\nspam.example' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save tracking settings' }))

    await waitFor(() => expect(patch).toHaveBeenCalledWith(
      '/api/private/sites/7',
      expect.objectContaining({
        body: { strict_origin: true, blocked_referrers: ['evil.example', 'spam.example'] },
      }),
    ))
  })

  it('patches only changed tracker fields', async () => {
    renderSettings()

    const hash = await screen.findByRole('switch', { name: 'Track hash routes' })
    toggle(hash)
    fireEvent.click(screen.getByRole('button', { name: 'Save tracking settings' }))

    await waitFor(() => expect(patch).toHaveBeenCalledWith(
      '/api/private/sites/7',
      expect.objectContaining({ body: { tracker: { hash: true } } }),
    ))
  })

  it('keeps failed changes ready to retry and shows the server message', async () => {
    patch.mockResolvedValue({
      error: { detail: 'tracker settings could not be saved' },
      response: { ok: false, status: 400 },
    })
    renderSettings()

    toggle(await screen.findByRole('switch', { name: 'Track hash routes' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save tracking settings' }))

    expect(await screen.findByText('tracker settings could not be saved')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Save tracking settings' })).toBeEnabled()
    expect(screen.getByRole('switch', { name: 'Track hash routes' })).toBeChecked()
  })

  it.each(['editor', 'viewer'] as const)('renders nothing for %s members', (role) => {
    const { container } = render(
      <QueryClientProvider client={new QueryClient()}>
        <TrackingSettings orgId={3} role={role} siteId={7} />
      </QueryClientProvider>,
    )
    expect(container).toBeEmptyDOMElement()
  })
})
