import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ReplayPlayer } from './ReplayPlayer'

let resizeCallback: ResizeObserverCallback | undefined
const disconnect = vi.fn()

beforeEach(() => {
  resizeCallback = undefined
  disconnect.mockClear()
  vi.stubGlobal('ResizeObserver', class {
    constructor(callback: ResizeObserverCallback) {
      resizeCallback = callback
    }

    observe() {}
    disconnect() { disconnect() }
  })
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

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

  it('resizes the mounted replay when its available stage changes', async () => {
    const resize = vi.fn()
    const mountPlayer = vi.fn(() => ({ goto: vi.fn(), resize }))
    const view = render(
      <ReplayPlayer
        load={() => Promise.resolve({
          events: [{ type: 4, timestamp: 1, data: { width: 1440, height: 900 } }],
          truncated: false,
        })}
        mountPlayer={mountPlayer}
      />,
    )
    await waitFor(() => expect(mountPlayer).toHaveBeenCalled())

    resizeCallback?.(
      [{ contentRect: { width: 800, height: 600 } } as ResizeObserverEntry],
      {} as ResizeObserver,
    )

    expect(resize).toHaveBeenCalledWith({ width: 800, height: 600 })
    view.unmount()
    expect(disconnect).toHaveBeenCalledOnce()
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
