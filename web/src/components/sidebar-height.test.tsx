import '@testing-library/jest-dom/vitest'

import { render } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { Sidebar, SidebarProvider } from './ui/sidebar'

describe('sidebar page height', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: vi.fn().mockReturnValue({
        matches: false,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    })
  })

  it('keeps the in-flow sidebar column painted on long pages', () => {
    const { container } = render(
      <SidebarProvider>
        <Sidebar />
        <main style={{ height: 2000 }} />
      </SidebarProvider>,
    )

    expect(container.querySelector('[data-slot="sidebar-gap"]')).toHaveClass('bg-sidebar')
  })

  it('keeps an icon-width column painted when collapsed to the rail', () => {
    const { container } = render(
      <SidebarProvider defaultOpen={false}>
        <Sidebar collapsible="icon" />
        <main style={{ height: 2000 }} />
      </SidebarProvider>,
    )
    expect(container.querySelector('[data-slot="sidebar"]')).toHaveAttribute('data-collapsible', 'icon')
    expect(container.querySelector('[data-slot="sidebar-gap"]')).toHaveClass('bg-sidebar', 'group-data-[collapsible=icon]:w-(--sidebar-width-icon)')
  })
})
