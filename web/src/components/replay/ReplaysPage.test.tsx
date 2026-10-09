import { renderAuthorized as render } from '@/test/authorization'
import '@testing-library/jest-dom/vitest'

import { cleanup, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { useReplays } = vi.hoisted(() => ({ useReplays: vi.fn() }))
vi.mock('./api', () => ({ useReplays }))
vi.mock('./ReplayLink', () => ({ ReplayLink: () => <span>Play replay</span> }))

import { ReplaysPage } from './ReplaysPage'

afterEach(cleanup)

describe('ReplaysPage', () => {
  it('renders visitor detail columns and replay activity', () => {
    useReplays.mockReturnValue({
      data: {
        items: [{
          session_id: 'session-1', started_at: '2026-10-08T08:00:00Z', duration_s: 75,
          pages: 3, bytes: 1024, trigger: 'sampled', country: 'IN', city: 'Bengaluru',
          browser: 'Chrome', os: 'Android', device: 'mobile', actions: 12,
        }],
      },
      isPending: false,
      isError: false,
      isFetching: false,
    })

    render(<ReplaysPage siteId={7} org="acme" site="example" from="2026-10-01" to="2026-10-08" page={1} onFirst={() => undefined} onNext={() => undefined} onPrevious={() => undefined} />)

    for (const name of ['Play', 'Started', 'Location', 'Browser', 'OS', 'Device', 'Actions', 'Duration', 'Pages', 'Trigger']) {
      expect(screen.getByRole('columnheader', { name })).toBeVisible()
    }
    expect(screen.getByText('Bengaluru, India')).toBeVisible()
    expect(screen.queryByText('IN')).not.toBeInTheDocument()
    expect(screen.getByText('Chrome')).toBeVisible()
    expect(screen.getByText('Android')).toBeVisible()
    expect(screen.getByText('Mobile')).toBeVisible()
    expect(screen.getByRole('cell', { name: '12' })).toBeVisible()
  })
})
