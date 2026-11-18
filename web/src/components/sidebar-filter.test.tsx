import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { Outlet, RouterProvider, createMemoryHistory, createRootRoute, createRoute, createRouter } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { Me, Membership } from '@/lib/me'

import { SupermetrySidebar } from './Sidebar'
import { filterGroups, flattenRows } from './sidebar/filter'
import type { MenuGroupData, MenuRow } from './sidebar/menu'
import { SidebarProvider } from './ui/sidebar'
import { TooltipProvider } from './ui/tooltip'

const membership: Membership = {
  org: { id: 1, name: 'Acme', slug: 'acme' },
  role: 'owner',
  effective_capabilities: ['profiles.read', 'replay.read'],
  sites: [{ id: 1, name: 'Docs', public_id: 'docs-public', tracking_policy: { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }],
}
const me: Me = { user: { id: 1, name: 'Owner', email: 'owner@supermetry.test' }, memberships: [membership] }

function renderSidebar(initialEntry = '/app/acme/docs-public/realtime', options: { defaultOpen?: boolean; onOpenPalette?: () => void; seed?: Record<string, boolean> } = {}) {
  localStorage.clear()
  if (options.seed) localStorage.setItem('sm.sidebar.v1.1', JSON.stringify({ open: options.seed, pinned: [] }))
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={new QueryClient()}>
        <TooltipProvider>
          <SidebarProvider defaultOpen={options.defaultOpen ?? true}>
            <SupermetrySidebar me={me} membership={membership} requestedSite="docs-public" onOpenPalette={options.onOpenPalette} />
            <Outlet />
            <input aria-label="Outside field" />
          </SidebarProvider>
        </TooltipProvider>
      </QueryClientProvider>
    ),
  })
  const splat = createRoute({ getParentRoute: () => rootRoute, path: '$', component: () => <h1>Page</h1> })
  const router = createRouter({ routeTree: rootRoute.addChildren([splat]), history: createMemoryHistory({ initialEntries: [initialEntry] }) })
  render(<RouterProvider router={router} />)
  return router
}

const field = () => screen.getByRole('searchbox', { name: 'Filter menu' })
const type = (value: string) => fireEvent.change(field(), { target: { value } })

beforeEach(() => {
  globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} }
  window.scrollTo = () => undefined
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: () => ({ matches: false, addEventListener: () => undefined, removeEventListener: () => undefined }),
  })
})
afterEach(() => { cleanup(); localStorage.clear() })

describe('filterGroups', () => {
  const row = (label: string, children?: MenuRow[]): MenuRow => ({ key: label, label, href: `/${label}`, active: false, render: () => null, children })
  const groups: MenuGroupData[] = [
    { key: 'a', label: 'Analyze', entries: [row('Events'), row('Settings', [row('General'), row('Alerts')])] },
    { key: 'b', label: 'Manage', entries: [row('Links')] },
  ]
  it('matches entries case-insensitively, drops empty groups and keeps the parent of a matching sub-item', () => {
    expect(filterGroups(groups, 'EVEN').map((g) => g.entries.map((e) => e.label))).toEqual([['Events']])
    const nested = filterGroups(groups, 'alert')
    expect(nested[0].entries[0].label).toBe('Settings')
    expect(nested[0].entries[0].children?.map((c) => c.label)).toEqual(['Alerts'])
    expect(flattenRows(nested).map((r) => r.label)).toEqual(['Settings', 'Alerts'])
  })
  it('keeps every row of a group whose name matches, and all sub-items of a matching parent', () => {
    expect(filterGroups(groups, 'manage')[0].entries.map((e) => e.label)).toEqual(['Links'])
    expect(filterGroups(groups, 'sett')[0].entries[0].children).toHaveLength(2)
    expect(filterGroups(groups, '  ')).toBe(groups)
  })
})

describe('sidebar menu filter', () => {
  it('filters across groups, hides empty ones, force-expands collapsed groups and highlights the match', async () => {
    renderSidebar('/app/acme/docs-public/realtime', { seed: { behaviour: false } })
    await screen.findByRole('link', { name: 'Realtime' })
    expect(screen.queryByRole('link', { name: 'Journeys' })).toBeNull()
    type('jour')
    const link = await screen.findByRole('link', { name: 'Journeys' })
    expect(link.querySelector('[data-match]')?.textContent).toBe('Jour')
    expect(link.querySelector('[data-match]')).toHaveClass('font-semibold', 'text-foreground')
    expect(screen.queryByRole('link', { name: 'Realtime' })).toBeNull()
    expect(screen.queryByRole('navigation', { name: 'Conversion' })).toBeNull()
    expect(screen.getByRole('status')).toHaveTextContent('1 match')
    // Clearing the field brings back the saved collapsed state.
    type('')
    expect(screen.queryByRole('link', { name: 'Journeys' })).toBeNull()
    expect(screen.getByRole('link', { name: 'Realtime' })).toBeInTheDocument()
  })

  it('matches group names and nested settings pages', async () => {
    renderSidebar()
    await screen.findByRole('link', { name: 'Realtime' })
    type('conver')
    const nav = screen.getByRole('navigation', { name: 'Conversion' })
    expect(within(nav).getAllByRole('link').map((a) => a.textContent)).toEqual(['Funnels', 'Goals', 'Revenue'])
    type('cron')
    const settings = screen.getByRole('navigation', { name: 'Settings' })
    expect(within(settings).getByRole('link', { name: 'Project settings' })).toBeInTheDocument()
    expect(within(settings).getByRole('link', { name: 'Cron monitors' })).toHaveAttribute('href', '/app/acme/docs-public/settings/monitors')
    expect(within(settings).queryByRole('link', { name: 'Tracking' })).toBeNull()
  })

  it('moves a visual cursor with the arrow keys, Enter navigates, Escape clears and blurs', async () => {
    const router = renderSidebar()
    await screen.findByRole('link', { name: 'Realtime' })
    field().focus()
    type('s')
    const rows = () => Array.from(document.querySelectorAll('[data-kbd="true"]'))
    expect(rows()).toHaveLength(1)
    const first = rows()[0]
    expect(field()).toHaveAttribute('aria-activedescendant', first.id)
    fireEvent.keyDown(field(), { key: 'ArrowDown' })
    expect(rows()[0]).not.toBe(first)
    fireEvent.keyDown(field(), { key: 'ArrowUp' })
    expect(rows()[0]).toBe(first)
    type('journeys')
    fireEvent.keyDown(field(), { key: 'Enter' })
    await waitFor(() => expect(router.state.location.pathname).toBe('/app/acme/docs-public/journeys'))
    expect(field()).toHaveValue('')
    type('goal')
    expect(field()).toHaveValue('goal')
    fireEvent.keyDown(field(), { key: 'Escape' })
    expect(field()).toHaveValue('')
    expect(field()).not.toHaveFocus()
  })

  it('focuses the field with / unless the user is typing, and shows the key', async () => {
    renderSidebar()
    await screen.findByRole('link', { name: 'Realtime' })
    expect(field()).toHaveAttribute('aria-keyshortcuts', '/')
    const outside = screen.getByRole('textbox', { name: 'Outside field' })
    outside.focus()
    fireEvent.keyDown(outside, { key: '/' })
    expect(field()).not.toHaveFocus()
    fireEvent.keyDown(document.body, { key: '/' })
    expect(field()).toHaveFocus()
  })

  it('offers "Search everything" when nothing matches and opens the palette', async () => {
    const onOpenPalette = vi.fn()
    renderSidebar('/app/acme/docs-public/realtime', { onOpenPalette })
    await screen.findByRole('link', { name: 'Realtime' })
    type('zzzz')
    expect(screen.getByText('No matches')).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('0 matches')
    fireEvent.click(screen.getByRole('button', { name: 'Search everything' }))
    expect(onOpenPalette).toHaveBeenCalledTimes(1)
  })

  it('falls back to the palette shortcut when no callback is passed', async () => {
    renderSidebar()
    await screen.findByRole('link', { name: 'Realtime' })
    const seen = vi.fn((event: KeyboardEvent) => event.key === 'k' && event.ctrlKey)
    document.addEventListener('keydown', seen)
    type('zzzz')
    fireEvent.click(screen.getByRole('button', { name: 'Search everything' }))
    document.removeEventListener('keydown', seen)
    expect(seen).toHaveReturnedWith(true)
  })

  it('becomes an icon button on the rail that expands the sidebar and focuses the field', async () => {
    renderSidebar('/app/acme/docs-public/realtime', { defaultOpen: false })
    await screen.findByRole('link', { name: 'Realtime' })
    expect(screen.queryByRole('searchbox')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Filter menu' }))
    await waitFor(() => expect(field()).toHaveFocus())
  })
})

describe('nested settings', () => {
  it('auto-expands project settings on a settings route and marks the active page', async () => {
    renderSidebar('/app/acme/docs-public/settings/tracking')
    const nav = await screen.findByRole('navigation', { name: 'Settings' })
    expect(within(nav).getByRole('link', { name: 'Project settings' })).toHaveAttribute('aria-current', 'page')
    const tracking = within(nav).getByRole('link', { name: 'Tracking' })
    expect(tracking).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: 'General' })).not.toHaveAttribute('aria-current')
    expect(tracking.closest('ul')).toHaveAttribute('data-slot', 'sidebar-menu-sub')
    // Organization settings stay collapsed until visited.
    expect(within(nav).queryByRole('link', { name: 'Members' })).toBeNull()
  })

  it('toggles a settings section by its chevron and remembers it', async () => {
    renderSidebar('/app/acme/docs-public/realtime')
    const nav = await screen.findByRole('navigation', { name: 'Settings' })
    expect(within(nav).queryByRole('link', { name: 'General' })).toBeNull()
    const toggle = within(nav).getByRole('button', { name: 'Expand Project settings' })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    fireEvent.click(toggle)
    expect(within(nav).getByRole('link', { name: 'General' })).toHaveAttribute('href', '/app/acme/docs-public/settings/general')
    expect(within(nav).getByRole('button', { name: 'Collapse Project settings' })).toHaveAttribute('aria-expanded', 'true')
    expect(JSON.parse(localStorage.getItem('sm.sidebar.v1.1') ?? '{}').open['settings:site']).toBe(true)
  })

  it('lists organization settings pages for owners', async () => {
    renderSidebar('/app/acme/settings/members')
    const nav = await screen.findByRole('navigation', { name: 'Settings' })
    expect(within(nav).getByRole('link', { name: 'Members' })).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: 'API keys' })).toHaveAttribute('href', '/app/acme/settings/api-keys')
  })
})
