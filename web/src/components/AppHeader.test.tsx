import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { useLiveSummary, useUpdateStatus } = vi.hoisted(() => ({ useLiveSummary: vi.fn(), useUpdateStatus: vi.fn() }))
vi.mock('@/features/realtime/useLiveSummary', () => ({ useLiveSummary }))
vi.mock('@/features/admin/adminApi', () => ({ useUpdateStatus }))

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SidebarProvider } from '@/components/ui/sidebar'
import { TooltipProvider } from '@/components/ui/tooltip'
import { renderWithRouter } from '@/test/renderWithRouter'

import { AppHeader } from './AppHeader'

beforeEach(() => {
  useUpdateStatus.mockReturnValue({ data: undefined, isError: false })
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: () => ({ matches: false, addEventListener: () => undefined, removeEventListener: () => undefined }),
  })
})
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

function show(path: string, onOpenPalette = vi.fn(), _locale: string = '', instanceAdmin = false, extra: Partial<React.ComponentProps<typeof AppHeader>> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  renderWithRouter(
    <QueryClientProvider client={client}>
    <TooltipProvider>
      <SidebarProvider>
        <AppHeader instanceAdmin={instanceAdmin} onOpenHelp={vi.fn()} onOpenAbout={vi.fn()} onOpenPalette={onOpenPalette} {...extra} />
      </SidebarProvider>
    </TooltipProvider>
    </QueryClientProvider>,
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
    expect(within(header).getByRole('button', { name: 'Help' })).toBeInTheDocument()
    expect(useLiveSummary).not.toHaveBeenCalled()
  })

  it('opens the command palette from a labelled trigger that announces its shortcut', async () => {
    const onOpenPalette = vi.fn()
    const header = await show('/app/account/profile', onOpenPalette)
    const trigger = within(header).getByRole('button', { name: 'Search or jump to…' })
    expect(trigger).toHaveAttribute('aria-keyshortcuts', 'Control+K Meta+K')
    fireEvent.click(trigger)
    expect(onOpenPalette).toHaveBeenCalledOnce()
  })

  it('shows no breadcrumbs for an organization the user has no membership in', async () => {
    const header = await show('/app/missing')
    expect(within(header).queryByRole('navigation', { name: 'Breadcrumb' })).toBeNull()
  })
})

describe('AppHeader live badge', () => {
  const membership = { org: { id: 1, name: 'Acme', slug: 'acme' }, role: 'owner' as const, sites: [] }
  beforeEach(() => useLiveSummary.mockReturnValue({ data: { count: 2 }, isPending: false }))
  it('shows the live badge for a web site but not for an app', async () => {
    await show('/app/acme/docs', vi.fn(), '', false, { membership, site: { id: 7, name: 'Docs', kind: 'web' }, requestedSite: 'docs' })
    expect(useLiveSummary).toHaveBeenCalled()
    cleanup()
    useLiveSummary.mockClear()
    await show('/app/acme/desk', vi.fn(), '', false, { membership, site: { id: 8, name: 'Desk', kind: 'app' }, requestedSite: 'desk' })
    expect(useLiveSummary).not.toHaveBeenCalled()
  })
})

describe('AppHeader update notice', () => {
  it('does not query or show an update notice for a non-admin', async () => {
    useUpdateStatus.mockReturnValue({
      data: { current_version: '1.2.0', latest_version: '1.3.0', update_available: true },
      isError: false,
    })
    const header = await show('/app/account/profile')

    expect(useUpdateStatus).toHaveBeenCalledWith(false)
    expect(within(header).queryByRole('link', { name: 'Update available' })).not.toBeInTheDocument()
  })

  it('shows admins an accessible update link when a release is available', async () => {
    useUpdateStatus.mockReturnValue({
      data: { current_version: '1.2.0', latest_version: '1.3.0', update_available: true },
      isError: false,
    })

    const header = await show('/app/admin/users', vi.fn(), '', true)
    const notice = within(header).getByRole('link', { name: 'Update available' })

    expect(useUpdateStatus).toHaveBeenCalledWith(true)
    expect(notice).toHaveTextContent('Update available')
    expect(notice).toHaveAttribute('href', '/app/admin/update')
    expect(notice.closest('[role="status"]')).toBeInTheDocument()
  })

  it('changes the admin notice while an update is running', async () => {
    useUpdateStatus.mockReturnValue({
      data: {
        current_version: '1.2.0', latest_version: '1.3.0', update_available: true,
        job: { status: 'activating' },
      },
      isError: false,
    })

    const header = await show('/app/admin/update', vi.fn(), '', true)
    expect(within(header).getByRole('link', { name: 'Update in progress' })).toHaveAttribute('href', '/app/admin/update')
    expect(within(header).queryByRole('link', { name: 'Update available' })).not.toBeInTheDocument()
  })

  it.each([
    ['the instance is current', { data: { current_version: '1.3.0', latest_version: '1.3.0', update_available: false }, isError: false }],
    ['the check failed', { data: undefined, isError: true }],
  ])('shows no update notice when %s', async (_name, result) => {
    useUpdateStatus.mockReturnValue(result)
    const header = await show('/app/admin/users', vi.fn(), '', true)
    expect(within(header).queryByRole('link', { name: 'Update available' })).not.toBeInTheDocument()
  })
})

describe('AppHeader controls', () => {
  it('has one search field with the shortcut inside it, plus help, and no language or theme buttons', async () => {
    const onOpenPalette = vi.fn()
    const header = await show('/app/account/profile', onOpenPalette)
    const field = within(header).getByRole('button', { name: 'Search or jump to…' })
    expect(field).toHaveAttribute('aria-keyshortcuts', 'Control+K Meta+K')
    // The hint sits beside the button's text (name equals visible text), inside the same field wrapper.
    expect(field.parentElement?.querySelector('kbd')).not.toBeNull()
    expect(field.querySelector('kbd')).toBeNull()
    expect(within(header).queryByRole('button', { name: 'Language' })).toBeNull()
    expect(within(header).queryByRole('button', { name: /theme/ })).toBeNull()
    expect(within(header).queryByRole('button', { name: 'Open command palette' })).toBeNull()
    fireEvent.click(field)
    expect(onOpenPalette).toHaveBeenCalledOnce()
  })

  it('opens About and the shortcuts dialog from the help menu', async () => {
    const onOpenHelp = vi.fn()
    const onOpenAbout = vi.fn()
    const header = await show('/app/account/profile', vi.fn(), '', false, { onOpenHelp, onOpenAbout })
    fireEvent.keyDown(within(header).getByRole('button', { name: 'Help' }), { key: 'ArrowDown' })
    fireEvent.click(await screen.findByRole('menuitem', { name: 'About Supermetry' }))
    expect(onOpenAbout).toHaveBeenCalledOnce()
    fireEvent.keyDown(within(header).getByRole('button', { name: 'Help' }), { key: 'ArrowDown' })
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Keyboard shortcuts' }))
    expect(onOpenHelp).toHaveBeenCalledOnce()
  })

  it('keeps the help control visible at phone width (no hidden-on-mobile class)', async () => {
    const header = await show('/app/account/profile')
    expect(within(header).getByRole('button', { name: 'Help' }).className).not.toContain('hidden')
  })
})
