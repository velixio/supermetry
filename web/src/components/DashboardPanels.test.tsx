import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createRef, useEffect } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { DashboardPanels, type InspectorPanel } from './DashboardPanels'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}

function viewport(width: number) {
  vi.stubGlobal('innerWidth', width)
}

function panel(overrides: Partial<InspectorPanel> = {}): InspectorPanel {
  return { key: 'country:IN', title: 'India', dimension: 'Country', body: <p>Body</p>, onClose: vi.fn(), ...overrides }
}

beforeEach(() => {
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
    expect(aside).toHaveClass('sticky', 'top-28')
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
})
