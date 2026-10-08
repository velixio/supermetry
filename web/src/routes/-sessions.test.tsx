import '@testing-library/jest-dom/vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  api: { GET: get },
}))

import { routeTree } from '@/routeTree.gen'
import { parseSearch, stringifySearch } from '@/router'

const me = {
  user: { id: 1, name: 'Viewer', email: 'viewer@example.com' },
  memberships: [{
    org: { id: 1, name: 'Acme', slug: 'acme' },
    role: 'viewer',
    sites: [{ id: 7, name: 'Example', slug: 'example', timezone: 'UTC' }],
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
    renderPath('/acme/example/sessions?period=custom&from=2026-10-01&to=2026-10-08&f=country%3Ais%3AIN')
    expect(await screen.findByRole('heading', { name: 'Sessions' })).toBeVisible()
    await waitFor(() => expect(get).toHaveBeenCalledWith(
      '/api/private/sites/{site_id}/sessions',
      expect.objectContaining({ params: expect.objectContaining({
        path: { site_id: 7 },
        query: expect.objectContaining({ from: '2026-10-01', to: '2026-10-08', filter: ['country:is:IN'] }),
      }) }),
    ))
  })
})
