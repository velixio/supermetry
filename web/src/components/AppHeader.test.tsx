import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { useLiveSummary } = vi.hoisted(() => ({ useLiveSummary: vi.fn() }))
vi.mock('@/features/realtime/useLiveSummary', () => ({ useLiveSummary }))

import { SidebarProvider } from '@/components/ui/sidebar'
import { TooltipProvider } from '@/components/ui/tooltip'
import { renderWithRouter } from '@/test/renderWithRouter'

import { AppHeader } from './AppHeader'

beforeEach(() => {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: () => ({ matches: false, addEventListener: () => undefined, removeEventListener: () => undefined }),
  })
})
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

function show(path: string, onOpenPalette = vi.fn()) {
  renderWithRouter(
    <TooltipProvider>
      <SidebarProvider>
        <AppHeader theme="light" onToggleTheme={vi.fn()} onOpenHelp={vi.fn()} onOpenPalette={onOpenPalette} />
      </SidebarProvider>
    </TooltipProvider>,
    path,
  )
  return screen.findByRole('banner', { name: 'Application header' })
}

describe('AppHeader outside an organization', () => {
  it('shows account crumbs and the header actions, without a live badge', async () => {
    const header = await show('/app/account/profile')
    const crumbs = within(header).getByRole('navigation', { name: 'Breadcrumb' })
    expect(within(crumbs).getByRole('link', { name: 'Account' })).toHaveAttribute('href', '/app/account')
    expect(within(crumbs).getByText('Profile')).toHaveAttribute('aria-current', 'page')
    expect(within(header).getByRole('button', { name: 'Show keyboard shortcuts' })).toBeInTheDocument()
    expect(within(header).getByRole('button', { name: 'Use dark theme' })).toBeInTheDocument()
    expect(useLiveSummary).not.toHaveBeenCalled()
  })

  it('opens the command palette from a labelled trigger that announces its shortcut', async () => {
    const onOpenPalette = vi.fn()
    const header = await show('/app/account/profile', onOpenPalette)
    const trigger = within(header).getByRole('button', { name: 'Open command palette' })
    expect(trigger).toHaveAttribute('aria-keyshortcuts', 'Control+K Meta+K')
    fireEvent.click(trigger)
    expect(onOpenPalette).toHaveBeenCalledOnce()
  })

  it('shows no breadcrumbs for an organization the user has no membership in', async () => {
    const header = await show('/app/missing')
    expect(within(header).queryByRole('navigation', { name: 'Breadcrumb' })).toBeNull()
  })
})
