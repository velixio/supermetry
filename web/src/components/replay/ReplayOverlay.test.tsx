import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { useSession } = vi.hoisted(() => ({ useSession: vi.fn() }))
vi.mock('@/features/sessions/api', () => ({ useSession }))
vi.mock('@/lib/replay-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/replay-api')>()),
  getReplayCSS: vi.fn().mockResolvedValue(''),
  getReplayData: vi.fn().mockResolvedValue({ events: [{ type: 4, timestamp: 1 }], truncated: false }),
}))

import { ReplayOverlay } from './ReplayOverlay'
import { renderAuthorizedWithRouter as renderWithRouter } from '@/test/authorization'

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('ReplayOverlay', () => {
  it('seeks the replay to the timeline offset from the first event', async () => {
    const goto = vi.fn()
    const mountPlayer = vi.fn(() => ({ destroy: vi.fn(), goto }))
    useSession.mockReturnValue({
      data: {
        session: {
          id: '1683029196918025731', started_at: '2026-10-08T08:00:00Z', duration_ms: 120000,
          pageviews: 2, events: 1, entry_page: '/', exit_page: '/pricing', source: 'Direct',
          country: 'IN', city: 'Bengaluru', device: 'desktop', browser: 'Chrome', os: 'Linux', has_replay: true,
        },
        timeline: [
          { ts: '2026-10-08T08:00:10.000Z', kind: 'pageview', name: 'pageview', page: '/' },
          { ts: '2026-10-08T08:01:10.250Z', kind: 'event', name: 'signup', page: '/pricing' },
        ],
      },
      isPending: false,
      isError: false,
    })

    renderWithRouter(
      <ReplayOverlay
        open
        mountPlayer={mountPlayer}
        org="acme"
        rows={[]}
        sessionId="1683029196918025731"
        site="example"
        siteId={7}
        onOpenChange={() => undefined}
        onSessionChange={() => undefined}
      />,
    )

    await waitFor(() => expect(mountPlayer).toHaveBeenCalled())
    fireEvent.click(screen.getByRole('button', { name: /signup/i }))

    expect(goto).toHaveBeenCalledWith(60250)
  })
})
