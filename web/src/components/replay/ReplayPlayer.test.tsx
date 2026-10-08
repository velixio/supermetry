import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen, waitFor } from '@testing-library/react'
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

	 it('rehydrates referenced stylesheets before mounting playback', async () => {
		const mountPlayer = vi.fn(() => () => undefined)
		const fetchCSS = vi.fn().mockResolvedValue('body { color: navy; }')
		render(
			<ReplayPlayer
				load={() => Promise.resolve({
					events: [{ type: 2, data: { node: { attributes: { _cssRef: 'abc123' } } } }],
					truncated: false,
				})}
				fetchCSS={fetchCSS}
				mountPlayer={mountPlayer}
			/>,
		)

		await waitFor(() => {
			expect(fetchCSS).toHaveBeenCalledWith('abc123')
			expect(mountPlayer).toHaveBeenCalledWith(expect.any(HTMLElement), [
				{ type: 2, data: { node: { attributes: { _cssText: 'body { color: navy; }' } } } },
			])
		})
	})
})
