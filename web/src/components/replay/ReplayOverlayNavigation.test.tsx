import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  useNavigate,
  useSearch,
} from '@tanstack/react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { parseSearch, stringifySearch } from '@/router'

const { useReplays, useSession, useSessions } = vi.hoisted(() => ({
  useReplays: vi.fn(),
  useSession: vi.fn(),
  useSessions: vi.fn(),
}))

vi.mock('./api', () => ({ useReplays }))
vi.mock('@/features/sessions/api', () => ({ useSession, useSessions }))
vi.mock('./ReplayPlayer', () => ({ ReplayPlayer: () => <div>Replay canvas</div> }))

import { ReplaysPage } from './ReplaysPage'
import { SessionsPage } from '@/features/sessions/SessionsPage'

const replayRows = [
  {
    session_id: '1683029196918025731', started_at: '2026-10-08T08:00:00Z', duration_s: 75,
    pages: 3, bytes: 1024, trigger: 'sampled', country: 'IN', city: 'Bengaluru',
    browser: 'Chrome', os: 'Android', device: 'mobile', actions: 12,
  },
  {
    session_id: '1683029196918025732', started_at: '2026-10-08T09:00:00Z', duration_s: 30,
    pages: 1, bytes: 512, trigger: 'sampled', country: 'US', city: 'Austin',
    browser: 'Firefox', os: 'Linux', device: 'desktop', actions: 4,
  },
] as const

const sessionRows = replayRows.map((row) => ({
  id: row.session_id,
  started_at: row.started_at,
  duration_ms: row.duration_s * 1000,
  pageviews: row.pages,
  events: row.actions - row.pages,
  entry_page: '/',
  exit_page: '/pricing',
  source: 'Direct',
  country: row.country,
  city: row.city,
  device: row.device,
  browser: row.browser,
  os: row.os,
  has_replay: true,
}))

type ReplayNavigate = (options: {
  search: (previous: Record<string, unknown>) => Record<string, unknown>
  replace: boolean
}) => Promise<void>

function navigateReplay(navigate: ReplayNavigate, replay?: string, replace = false) {
  void navigate({ search: (previous) => ({ ...previous, replay }), replace })
}

function ReplaysHarness() {
  const search = useSearch({ strict: false }) as { replay?: string }
  const navigate = useNavigate() as unknown as ReplayNavigate
  return (
    <ReplaysPage
      from="2026-10-01"
      org="acme"
      replayId={search.replay}
      site="example"
      siteId={7}
      to="2026-10-08"
      onCursorChange={() => undefined}
      onReplayChange={(replay, options) => navigateReplay(navigate, replay, options?.replace)}
    />
  )
}

function SessionsHarness() {
  const search = useSearch({ strict: false }) as { replay?: string }
  const navigate = useNavigate() as unknown as ReplayNavigate
  return (
    <SessionsPage
      dateRange={{ from: '2026-10-01', to: '2026-10-08' }}
      filters={[]}
      org="acme"
      replayId={search.replay}
      site="example"
      siteId={7}
      onCursorChange={() => undefined}
      onReplayChange={(replay, options) => navigateReplay(navigate, replay, options?.replace)}
      onSelect={() => undefined}
    />
  )
}

function renderRoute(component: () => ReactNode, initialEntries = ['/']) {
  const root = createRootRoute()
  const index = createRoute({ getParentRoute: () => root, path: '/', component })
  const history = createMemoryHistory({ initialEntries })
  const router = createRouter({ routeTree: root.addChildren([index]), history, parseSearch, stringifySearch })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)
  return router
}

describe('replay overlay URL navigation', () => {
  beforeEach(() => {
    useReplays.mockReturnValue({ data: { items: replayRows, next_cursor: null }, isPending: false, isError: false, isFetching: false })
    useSessions.mockReturnValue({ data: { data: sessionRows, next_cursor: null }, isPending: false, isError: false, isFetching: false })
    useSession.mockImplementation((_siteId: number, sessionId?: string) => ({
      data: sessionId ? { session: sessionRows.find((row) => row.id === sessionId) ?? sessionRows[0], timeline: [] } : undefined,
      isPending: false,
      isError: false,
    }))
  })

  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('opens on the current Replays page and preserves a 64-bit session ID exactly', async () => {
    const router = renderRoute(ReplaysHarness)

    fireEvent.click((await screen.findAllByRole('button', { name: 'Play replay' }))[0])

    expect(await screen.findByRole('dialog', { name: 'Session replay' })).toBeVisible()
    expect(router.state.location.pathname).toBe('/')
    expect((router.state.location.search as { replay?: string }).replay).toBe('1683029196918025731')
    expect(router.state.location.href).toContain('replay=1683029196918025731')
  })

  it('closes on Escape and removes the replay search parameter', async () => {
    const router = renderRoute(ReplaysHarness, ['/', '/?replay=1683029196918025731'])
    expect(await screen.findByRole('dialog', { name: 'Session replay' })).toBeVisible()

    fireEvent.keyDown(document, { key: 'Escape' })

    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Session replay' })).not.toBeInTheDocument())
    expect((router.state.location.search as { replay?: string }).replay).toBeUndefined()
  })

  it('closes when browser history goes back from an opened replay', async () => {
    const router = renderRoute(ReplaysHarness)
    fireEvent.click((await screen.findAllByRole('button', { name: 'Play replay' }))[0])
    expect(await screen.findByRole('dialog', { name: 'Session replay' })).toBeVisible()

    router.history.back()

    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Session replay' })).not.toBeInTheDocument())
    expect((router.state.location.search as { replay?: string }).replay).toBeUndefined()
  })

  it('opens from a shared URL and moves only through visible replay rows', async () => {
    const router = renderRoute(ReplaysHarness, ['/?replay=1683029196918025731'])
    expect(await screen.findByRole('dialog', { name: 'Session replay' })).toBeVisible()

    expect(screen.getByRole('button', { name: 'Previous replay' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Next replay' })).toBeEnabled()

    fireEvent.click(screen.getByRole('button', { name: 'Next replay' }))
    await waitFor(() => expect((router.state.location.search as { replay?: string }).replay).toBe('1683029196918025732'))
    expect(screen.getByRole('button', { name: 'Previous replay' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Next replay' })).toBeDisabled()

    fireEvent.click(screen.getByRole('button', { name: 'Previous replay' }))
    await waitFor(() => expect((router.state.location.search as { replay?: string }).replay).toBe('1683029196918025731'))
  })

  it('opens the replay overlay from Sessions', async () => {
    const router = renderRoute(SessionsHarness)

    fireEvent.click((await screen.findAllByRole('button', { name: 'Play replay' }))[0])

    expect(await screen.findByRole('dialog', { name: 'Session replay' })).toBeVisible()
    expect((router.state.location.search as { replay?: string }).replay).toBe('1683029196918025731')
  })
})
