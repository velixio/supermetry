import '@testing-library/jest-dom/vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  api: { GET: get },
}))

import { routeTree } from '@/routeTree.gen'
import { parseSearch, stringifySearch } from '@/router'
import { parseSessionsSearch } from './app.$org.$site.sessions'

const me = {
  user: { id: 1, name: 'Viewer', email: 'viewer@example.com' },
  memberships: [{
    org: { id: 1, name: 'Acme', slug: 'acme' },
    role: 'viewer',
    effective_capabilities: ['profiles.read', 'replay.read'],
    sites: [{ id: 7, name: 'Example', slug: 'example', timezone: 'UTC', tracking_policy: { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }],
  }],
}

function renderPath(path: string) {
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [path] }),
    parseSearch,
    stringifySearch,
  })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={queryClient}><RouterProvider router={router} /></QueryClientProvider>)
  return router
}

const session = {
  id: '301',
  started_at: '2026-10-08T08:00:00Z',
  duration_ms: 120000,
  pageviews: 2,
  events: 1,
  entry_page: '/',
  exit_page: '/pricing',
  source: 'Google',
  country: 'US',
  city: 'San Francisco',
  device: 'desktop',
  browser: 'Chrome',
  os: 'Linux',
  external_id: 'ada-42',
}

describe('sessions route', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: vi.fn().mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }),
    })
    Object.defineProperty(window, 'scrollTo', { configurable: true, value: vi.fn() })
    get.mockImplementation((path: string) => Promise.resolve({
      data: path === '/api/private/me' ? me : { data: [], next_cursor: null },
      response: new Response(null, { status: 200 }),
    }))
  })

  afterEach(cleanup)

  it('loads sessions for a viewer using URL dates and filters', async () => {
    renderPath('/app/acme/example/sessions?period=custom&from=2026-10-01&to=2026-10-08&f=country%3Ais%3AIN')
    expect(await screen.findByRole('heading', { name: 'Sessions' })).toBeVisible()
    await waitFor(() => expect(get).toHaveBeenCalledWith(
      '/api/private/sites/{site_id}/sessions',
      expect.objectContaining({ params: expect.objectContaining({
        path: { site_id: 7 },
        query: expect.objectContaining({ from: '2026-10-01', to: '2026-10-08', filter: ['country:is:IN'] }),
      }) }),
    ))
  })

  it('ignores unknown parameters and accepts legacy shared no-ops', async () => {
    const router = renderPath('/app/acme/example/sessions?period=7d&compare=previous_period&metric=pageviews&include_imported=false&live=true&unknown=value')

    expect(await screen.findByRole('heading', { name: 'Sessions' })).toBeVisible()
    const validatedSearch = parseSessionsSearch(router.state.location.search)
    expect(validatedSearch).toMatchObject({ period: '7d', compare: 'previous_period' })
    expect(validatedSearch).toMatchObject({ metric: 'pageviews', include_imported: false, live: true })
    expect(validatedSearch).not.toHaveProperty('unknown')
  })

  it('returns to page 1 and the original cursor after Next then Previous', async () => {
    get.mockImplementation((path: string) => Promise.resolve({
      data: path === '/api/private/me' ? me : { data: [session], next_cursor: 'cursor-2', total: 2, total_capped: false },
      response: new Response(null, { status: 200 }),
    }))
    const router = renderPath('/app/acme/example/sessions')

    fireEvent.click(await screen.findByRole('button', { name: 'Next' }))
    await waitFor(() => expect(router.state.location.search).toMatchObject({ page: '2', cursors: ['cursor-2'] }))
    await waitFor(() => expect(get).toHaveBeenCalledWith(
      '/api/private/sites/{site_id}/sessions',
      expect.objectContaining({ params: expect.objectContaining({ query: expect.objectContaining({ cursor: 'cursor-2' }) }) }),
    ))

    fireEvent.click(screen.getByRole('button', { name: 'Previous' }))
    await waitFor(() => expect(router.state.location.search).toMatchObject({ page: '1' }))
    expect(router.state.location.search).not.toHaveProperty('cursors')
    await waitFor(() => {
      const calls = get.mock.calls.filter(([path]) => path === '/api/private/sites/{site_id}/sessions')
      expect(calls.at(-1)?.[1]).toEqual(expect.objectContaining({
        params: expect.objectContaining({ query: expect.objectContaining({ cursor: undefined }) }),
      }))
    })
  })

  it('resets pagination when a filter changes', async () => {
    get.mockImplementation((path: string) => Promise.resolve({
      data: path === '/api/private/me' ? me : { data: [session], next_cursor: null, total: 2, total_capped: false },
      response: new Response(null, { status: 200 }),
    }))
    const router = renderPath('/app/acme/example/sessions?page=2&cursors=cursor-2')

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Has replay' }))

    await waitFor(() => expect(router.state.location.search).toMatchObject({ page: '1', has_replay: true }))
    expect(router.state.location.search).not.toHaveProperty('cursors')
  })

  it('keeps pagination when the replay overlay opens', async () => {
    get.mockImplementation((path: string) => Promise.resolve({
      data: path === '/api/private/me' ? me : { data: [{ ...session, has_replay: true }], next_cursor: null, total: 2, total_capped: false },
      response: new Response(null, { status: 200 }),
    }))
    const router = renderPath('/app/acme/example/sessions?page=2&cursors=cursor-2')

    const table = await screen.findByRole('table', { name: 'Sessions' })
    fireEvent.click(within(table).getByRole('button', { name: 'Play replay' }))

    await waitFor(() => expect(router.state.location.search).toMatchObject({ page: '2', replay: '301' }))
    expect(router.state.location.search).toHaveProperty('cursors')
    expect(router.state.location.search).not.toHaveProperty('has_replay', true)
  })
})
