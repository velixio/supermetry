import '@testing-library/jest-dom/vitest'

import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createRef, useEffect } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { DashboardPanels, type InspectorPanel } from './DashboardPanels'

// Container width the stub reports on observe(); tests change it and call resizeContainer().
let containerWidth = 1200
let observerFires = true
const observers = new Set<ResizeObserverStub>()
const disconnected = vi.fn()

class ResizeObserverStub {
  constructor(private callback: ResizeObserverCallback) {
    observers.add(this)
  }
  observe() {
    if (observerFires) this.fire()
  }
  unobserve() {}
  disconnect() {
    observers.delete(this)
    disconnected()
  }
  fire() {
    const size = [{ inlineSize: containerWidth, blockSize: 600 }]
    const entry = { contentRect: { width: containerWidth, height: 600 }, borderBoxSize: size, contentBoxSize: size, devicePixelContentBoxSize: size }
    this.callback([entry as unknown as ResizeObserverEntry], this as unknown as ResizeObserver)
  }
}

function resizeContainer(width: number) {
  containerWidth = width
  act(() => observers.forEach((observer) => observer.fire()))
}

function viewport(width: number) {
  vi.stubGlobal('innerWidth', width)
}

function panel(overrides: Partial<InspectorPanel> = {}): InspectorPanel {
  return { key: 'country:IN', title: 'India', dimension: 'Country', body: <p>Body</p>, onClose: vi.fn(), ...overrides }
}

beforeEach(() => {
  containerWidth = 1200
  observerFires = true
  observers.clear()
  disconnected.mockClear()
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
  vi.stubGlobal('matchMedia', vi.fn().mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }))
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  window.localStorage.clear()
})

describe('DashboardPanels', () => {
  it('shows the inspector beside main from 1280px, focuses its heading and offers a labelled resize handle', async () => {
    viewport(1440)
    render(<DashboardPanels main={<p>Main</p>} inspector={panel()} returnFocusRef={createRef()} />)

    const aside = screen.getByRole('complementary', { name: 'India' })
    expect(aside).toHaveClass('sticky')
    expect(screen.getByText('Country')).toBeVisible()
    await waitFor(() => expect(screen.getByRole('heading', { level: 2, name: 'India' })).toHaveFocus())
    expect(screen.getByRole('separator', { name: 'Resize inspector' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.getByText('Main')).toBeVisible()
  })

  it('closes with Esc or the close button beside main, but leaves an Esc another layer handled', () => {
    viewport(1440)
    const onClose = vi.fn()
    const body = <button type="button" onKeyDown={(event) => event.preventDefault()}>Nested</button>
    render(<DashboardPanels main={<p>Main</p>} inspector={panel({ onClose, body })} returnFocusRef={createRef()} />)

    fireEvent.keyDown(screen.getByRole('button', { name: 'Nested' }), { key: 'Escape' })
    expect(onClose).not.toHaveBeenCalled()
    fireEvent.keyDown(screen.getByRole('heading', { name: 'India' }), { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: 'Close inspector' }))
    expect(onClose).toHaveBeenCalledTimes(2)
  })

  it('keeps main mounted and returns focus to the invoking control when the side inspector closes', async () => {
    viewport(1440)
    const mounts = vi.fn()
    function Probe() {
      useEffect(() => mounts(), [])
      return <p>Main</p>
    }
    const origin = document.createElement('button')
    document.body.append(origin)
    const returnFocusRef = { current: origin as HTMLElement | null }
    const { rerender } = render(<DashboardPanels main={<Probe />} returnFocusRef={returnFocusRef} />)
    rerender(<DashboardPanels main={<Probe />} inspector={panel()} returnFocusRef={returnFocusRef} />)
    await waitFor(() => expect(screen.getByRole('heading', { name: 'India' })).toHaveFocus())
    rerender(<DashboardPanels main={<Probe />} returnFocusRef={returnFocusRef} />)

    await waitFor(() => expect(origin).toHaveFocus())
    expect(screen.queryByRole('complementary')).toBeNull()
    expect(mounts).toHaveBeenCalledTimes(1)
    origin.remove()
  })

  it('overlays a right-hand sheet between 768 and 1279px and closes it with Esc', async () => {
    viewport(1024)
    const onClose = vi.fn()
    render(<DashboardPanels main={<p>Main</p>} inspector={panel({ onClose })} returnFocusRef={createRef()} />)

    const sheet = await screen.findByRole('dialog', { name: 'India' })
    expect(sheet).toHaveAttribute('data-side', 'right')
    expect(sheet).toHaveAccessibleDescription('Country')
    expect(screen.queryByRole('separator')).toBeNull()
    expect(screen.getAllByRole('button', { name: /close/i })).toHaveLength(1)
    fireEvent.keyDown(sheet, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })

  it('uses a full-width bottom sheet at 85% height below 768px', async () => {
    viewport(390)
    render(<DashboardPanels main={<p>Main</p>} inspector={panel()} returnFocusRef={createRef()} />)

    const sheet = await screen.findByRole('dialog', { name: 'India' })
    expect(sheet).toHaveAttribute('data-side', 'bottom')
    expect(sheet).toHaveClass('data-[side=bottom]:h-[85dvh]')
  })

  it('returns focus to the invoking control when the sheet closes', async () => {
    viewport(1024)
    const origin = document.createElement('button')
    document.body.append(origin)
    const returnFocusRef = { current: origin as HTMLElement | null }
    const { rerender } = render(<DashboardPanels main={<p>Main</p>} inspector={panel()} returnFocusRef={returnFocusRef} />)
    await screen.findByRole('dialog', { name: 'India' })
    rerender(<DashboardPanels main={<p>Main</p>} returnFocusRef={returnFocusRef} />)

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(origin).toHaveFocus())
    origin.remove()
  })

  // 40rem main + 1.25rem handle + 20rem minimum inspector = 61.25rem = 980px at the 16px root.
  it.each([
    [979, 'dialog'],
    [980, 'complementary'],
  ])('at a %ipx container picks the %s layout', async (width, role) => {
    viewport(1440)
    containerWidth = width
    render(<DashboardPanels main={<p>Main</p>} inspector={panel()} returnFocusRef={createRef()} />)
    expect(await screen.findByRole(role)).toBeInTheDocument()
    expect(screen.queryByRole(role === 'dialog' ? 'complementary' : 'dialog')).toBeNull()
  })

  it('flips between side panel and sheet when the container resizes after mount', async () => {
    viewport(1440)
    render(<DashboardPanels main={<p>Main</p>} inspector={panel()} returnFocusRef={createRef()} />)
    expect(screen.getByRole('complementary')).toBeInTheDocument()
    resizeContainer(900)
    expect(await screen.findByRole('dialog', { name: 'India' })).toBeInTheDocument()
    expect(screen.queryByRole('complementary')).toBeNull()
    resizeContainer(1100)
    expect(await screen.findByRole('complementary')).toBeInTheDocument()
  })

  it('disconnects the container observer on unmount', () => {
    viewport(1440)
    const { unmount } = render(<DashboardPanels main={<p>Main</p>} returnFocusRef={createRef()} />)
    // The panel library observes too; every observer, ours included, must be gone after unmount.
    expect(observers.size).toBeGreaterThanOrEqual(1)
    unmount()
    expect(disconnected).toHaveBeenCalled()
    expect(observers.size).toBe(0)
  })

  it.each([
    [1279, 'dialog', 'right'],
    [1280, 'complementary', undefined],
    [767, 'dialog', 'bottom'],
    [768, 'dialog', 'right'],
  ])('at a %ipx viewport renders %s (%s)', async (width, role, side) => {
    viewport(width)
    render(<DashboardPanels main={<p>Main</p>} inspector={panel()} returnFocusRef={createRef()} />)
    const element = await screen.findByRole(role)
    if (side) expect(element).toHaveAttribute('data-side', side)
  })

  it('does not steal focus after the viewport narrows and widens again while open', async () => {
    viewport(1440)
    const matchMedia = window.matchMedia as unknown as ReturnType<typeof vi.fn>
    const handlers: Array<() => void> = []
    matchMedia.mockReturnValue({
      matches: false,
      addEventListener: (_: string, handler: () => void) => handlers.push(handler),
      removeEventListener: vi.fn(),
    })
    const origin = document.createElement('button')
    document.body.append(origin)
    const returnFocusRef = { current: origin as HTMLElement | null }
    const { rerender } = render(<DashboardPanels main={<p>Main</p>} inspector={panel()} returnFocusRef={returnFocusRef} />)
    await waitFor(() => expect(screen.getByRole('heading', { name: 'India' })).toHaveFocus())
    viewport(1024)
    act(() => handlers.forEach((handler) => handler()))
    await screen.findByRole('dialog')
    rerender(<DashboardPanels main={<p>Main</p>} returnFocusRef={returnFocusRef} />)
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(origin).toHaveFocus())
    origin.blur()
    viewport(1440)
    act(() => handlers.forEach((handler) => handler()))
    // The side layout must not treat the earlier open state as still pending.
    expect(origin).not.toHaveFocus()
    origin.remove()
  })

  it('sticks the inspector below the header and the measured filter row', () => {
    viewport(1440)
    render(<DashboardPanels main={<p>Main</p>} inspector={panel()} returnFocusRef={createRef()} />)
    const aside = screen.getByRole('complementary')
    expect(aside).toHaveClass('sticky', 'top-[calc(2.75rem+var(--dashboard-filter-h,3.75rem)+0.5rem)]')
    expect(aside).toHaveClass('max-h-[calc(100dvh-2.75rem-var(--dashboard-filter-h,3.75rem)-1rem)]')
  })

  describe('stable tree', () => {
    const mounts = vi.fn()
    function Probe() {
      useEffect(() => mounts(), [])
      return <p>Main</p>
    }
    beforeEach(() => mounts.mockClear())

    it('never remounts main when the container crosses the threshold, open or closed', async () => {
      viewport(1440)
      const props = { main: <Probe />, returnFocusRef: createRef<HTMLElement>() }
      const { rerender } = render(<DashboardPanels {...props} />)
      resizeContainer(900)
      resizeContainer(1100)
      rerender(<DashboardPanels {...props} inspector={panel()} />)
      resizeContainer(900)
      await screen.findByRole('dialog')
      resizeContainer(1100)
      await screen.findByRole('complementary')
      expect(mounts).toHaveBeenCalledTimes(1)
    })

    it('never remounts main when the viewport crosses 1280 and back', async () => {
      viewport(1440)
      const matchMedia = window.matchMedia as unknown as ReturnType<typeof vi.fn>
      const handlers: Array<() => void> = []
      matchMedia.mockReturnValue({
        matches: false,
        addEventListener: (_: string, handler: () => void) => handlers.push(handler),
        removeEventListener: vi.fn(),
      })
      render(<DashboardPanels main={<Probe />} inspector={panel()} returnFocusRef={createRef()} />)
      viewport(1024)
      act(() => handlers.forEach((handler) => handler()))
      await screen.findByRole('dialog')
      viewport(1440)
      act(() => handlers.forEach((handler) => handler()))
      await screen.findByRole('complementary')
      expect(mounts).toHaveBeenCalledTimes(1)
    })

    it('uses the synchronous pre-paint measurement for the first layout', async () => {
      viewport(1440)
      observerFires = false
      vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 900 } as DOMRect)
      render(<DashboardPanels main={<Probe />} inspector={panel()} returnFocusRef={createRef()} />)
      expect(screen.queryByRole('complementary')).toBeNull()
      expect(await screen.findByRole('dialog')).toBeInTheDocument()
      expect(mounts).toHaveBeenCalledTimes(1)
      vi.restoreAllMocks()
    })
  })
})
