import '@testing-library/jest-dom/vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  api: { GET: get, POST: post },
}))

import { routeTree } from '@/routeTree.gen'

const me = {
  user: { id: 1, name: 'Owner', email: 'owner@example.com' },
  memberships: [
    {
      org: { id: 1, name: 'Acme', slug: 'acme' },
      role: 'owner',
      sites: [{ id: 7, name: 'Example', slug: 'example' }],
    },
  ],
}

describe('dashboard breakdown route', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: vi.fn().mockReturnValue({
        matches: false,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    })
    Object.defineProperty(window, 'scrollTo', {
      configurable: true,
      value: vi.fn(),
    })
    get.mockResolvedValue({ data: me, response: new Response(null, { status: 200 }) })
    post.mockImplementation(
      (_path: string, options: { body: { dimensions?: string[]; metrics: string[] } }) => {
        const dimension = options.body.dimensions?.[0] ?? 'page'
        const metric = options.body.metrics[0]
        const label =
          dimension === 'country'
            ? 'IN'
            : dimension === 'page'
              ? '/'
              : dimension === 'time'
                ? '2026-09-28'
                : 'Direct'
        const totals = Object.fromEntries(
          options.body.metrics.map((requestedMetric) => [requestedMetric, 5]),
        )
        return Promise.resolve({
          data: {
            columns: [dimension, metric],
            rows: [{ [dimension]: label, [metric]: 5 }],
            totals,
            meta: { sources_used: ['daily'], cached: false },
          },
          response: new Response(null, { status: 200 }),
        })
      },
    )
  })

  afterEach(cleanup)

  it('clicking a country row adds the is filter to router search', async () => {
    const history = createMemoryHistory({
      initialEntries: [
        '/acme/example?period=custom&from=2026-09-28&to=2026-09-30',
      ],
    })
    const router = createRouter({ routeTree, history })
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )

    await waitFor(() =>
      expect(screen.getByRole('button', { name: /visitors/i })).toHaveTextContent('5'),
    )
    expect(screen.getByRole('combobox', { name: 'Compare' })).toBeVisible()
    expect(screen.getByRole('table', { name: /visitors over time/i })).toBeVisible()

    fireEvent.keyDown(document, { key: 'm' })
    await waitFor(() => expect(router.state.location.search.period).toBe('30d'))

    fireEvent.click(await screen.findByRole('button', { name: /filter by country IN/i }))

    await waitFor(() =>
      expect(router.state.location.search.f).toEqual(['country:is:IN']),
    )
  })
})
