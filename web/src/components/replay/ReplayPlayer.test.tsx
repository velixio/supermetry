import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ReplayPlayer } from './ReplayPlayer'

afterEach(cleanup)

describe('ReplayPlayer', () => {
  it('shows a truncation warning while retaining valid replay events', async () => {
    const mountPlayer = vi.fn(() => () => undefined)
    render(
      <ReplayPlayer
        load={() => Promise.resolve({ events: [{ type: 4, timestamp: 1 }], truncated: true })}
        mountPlayer={mountPlayer}
      />,
    )

    expect(await screen.findByText('Recording truncated')).toBeVisible()
    expect(mountPlayer).toHaveBeenCalledWith(expect.any(HTMLElement), [{ type: 4, timestamp: 1 }])
  })
})
