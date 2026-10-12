import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import type { Me, Membership } from '@/lib/me'

import { SupermetrySidebar } from './Sidebar'
import { RoutePending } from './RoutePending'
import { SidebarProvider } from './ui/sidebar'
import { TooltipProvider } from './ui/tooltip'

afterEach(cleanup)

const membership: Membership = {
  org: { id: 1, name: 'Acme', slug: 'acme' },
  role: 'owner',
  effective_capabilities: ['profiles.read', 'replay.read'],
  sites: [{ id: 1, name: 'Docs', public_id: 'docs-public', tracking_policy: { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }],
}

const me: Me = {
  user: { id: 1, name: 'Owner', email: 'owner@supermetry.test' },
  memberships: [membership],
}

function renderSidebar(
  initialEntry = '/app/acme/docs-public/realtime',
  options: { membership?: Membership; me?: Me; defaultOpen?: boolean } = {},
) {
  const sidebarMembership = options.membership ?? membership
  const sidebarMe = options.me ?? { ...me, memberships: [sidebarMembership] }
  const rootRoute = createRootRoute({
    component: () => (
      <TooltipProvider>
        <SidebarProvider defaultOpen={options.defaultOpen ?? true}>
          <SupermetrySidebar me={sidebarMe} membership={sidebarMembership} requestedSite="docs-public" />
          <Outlet />
        </SidebarProvider>
      </TooltipProvider>
    ),
  })
  const appRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: 'app',
    component: Outlet,
  })
  const siteRoute = createRoute({
    getParentRoute: () => appRoute,
    path: '$org/$site',
    component: () => <Outlet />,
  })
  const siteIndexRoute = createRoute({
    getParentRoute: () => siteRoute,
    path: '/',
    component: () => <h1>Dashboard page</h1>,
  })
  const siteSectionRoute = createRoute({
    getParentRoute: () => siteRoute,
    path: '$section',
    component: () => <h1>Section page</h1>,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([
      appRoute.addChildren([siteRoute.addChildren([siteIndexRoute, siteSectionRoute])]),
    ]),
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })

  render(<RouterProvider router={router} />)
  return router
}

describe('SPA navigation', () => {
  beforeEach(() => {
    window.scrollTo = () => undefined
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: () => ({
        matches: false,
        addEventListener: () => undefined,
        removeEventListener: () => undefined,
      }),
    })
  })

  it('uses the client router for sidebar links and exposes the active route', async () => {
    const router = renderSidebar()

    const realtime = await screen.findByRole('link', { name: 'Realtime' })
    expect(realtime).toHaveAttribute('aria-current', 'page')

    fireEvent.click(screen.getByRole('link', { name: 'Dashboard' }))

    await waitFor(() => expect(router.state.location.pathname).toBe('/app/acme/docs-public'))
    expect(screen.getByRole('heading', { name: 'Dashboard page' })).toBeInTheDocument()
  })

  it('places permission-gated Replays immediately after Realtime without a Map destination', async () => {
    renderSidebar()

    const realtime = await screen.findByRole('link', { name: 'Realtime' })
    const replays = screen.getByRole('link', { name: 'Replays' })
    expect(realtime.closest('li')?.nextElementSibling).toBe(replays.closest('li'))
    expect(screen.queryByRole('link', { name: 'Map' })).not.toBeInTheDocument()
  })

  it('groups navigation into labelled 44px-tall sections without changing item order', async () => {
    renderSidebar()

    const expected = {
      Analyze: ['Dashboard', 'Events', 'Sessions', 'Realtime', 'Replays'],
      Behaviour: ['Journeys', 'Retention', 'Profiles', 'Cohorts', 'Heatmaps', 'Web Vitals'],
      Conversion: ['Funnels', 'Goals', 'Revenue'],
      Manage: ['Sites', 'Links and pixels', 'Data health'],
    }
    for (const [name, links] of Object.entries(expected)) {
      const navigation = await screen.findByRole('navigation', { name })
      expect(Array.from(navigation.querySelectorAll('a')).map((link) => link.textContent?.trim())).toEqual(links)
      for (const link of navigation.querySelectorAll('a')) expect(link).toHaveClass('h-11')
    }
  })

  it('shows the brand name and one dropdown listing projects, then organizations', async () => {
    renderSidebar()

    expect(await screen.findByText('Supermetry')).toBeInTheDocument()
    const trigger = screen.getByRole('button', { name: /Docs\s*Acme/ })
    fireEvent.pointerDown(trigger, { button: 0, ctrlKey: false, pointerType: 'mouse' })

    const menu = await screen.findByRole('menu')
    const text = menu.textContent ?? ''
    expect(text.indexOf('Projects')).toBeGreaterThanOrEqual(0)
    expect(text.indexOf('Projects')).toBeLessThan(text.indexOf('Docs'))
    expect(text.indexOf('Docs')).toBeLessThan(text.indexOf('Organizations'))
    expect(text.indexOf('Organizations')).toBeLessThan(text.lastIndexOf('Acme'))
    expect(screen.getByRole('menuitem', { name: /Add organization/ })).toHaveAttribute('href', '/app/account/organizations/new')
  })

  it('renders a stable skeleton fallback for delayed route chunks', () => {
    render(<RoutePending />)

    expect(screen.getByRole('status', { name: 'Loading page' })).toHaveAttribute(
      'aria-busy',
      'true',
    )
    expect(document.querySelectorAll('[data-slot="skeleton"]')).not.toHaveLength(0)
  })

  it.each([
    ['owner', ['Site settings', 'Organization settings']],
    ['admin', ['Site settings', 'Organization settings']],
    ['editor', ['Site settings', 'Organization settings']],
    ['viewer', ['Organization settings']],
  ] as const)('lists settings for a %s in a group after Manage', async (role, labels) => {
    renderSidebar('/app/acme/docs-public', { membership: { ...membership, role } })
    const settings = await screen.findByRole('navigation', { name: 'Settings' })
    expect(Array.from(settings.querySelectorAll('a')).map((link) => link.textContent?.trim())).toEqual(labels)
    expect(within(settings).getByRole('link', { name: 'Organization settings' })).toHaveAttribute('href', '/app/acme/settings')
    if (role !== 'viewer') {
      expect(within(settings).getByRole('link', { name: 'Site settings' })).toHaveAttribute('href', '/app/acme/docs-public/settings/general')
    }
    const manage = screen.getByRole('navigation', { name: 'Manage' })
    expect(manage.compareDocumentPosition(settings) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    for (const link of settings.querySelectorAll('a')) expect(link).toHaveClass('h-11')
  })

  it('marks site settings as the current page on a settings route', async () => {
    renderSidebar('/app/acme/docs-public/settings')
    const settings = await screen.findByRole('navigation', { name: 'Settings' })
    expect(within(settings).getByRole('link', { name: 'Site settings' })).toHaveAttribute('aria-current', 'page')
    expect(within(settings).getByRole('link', { name: 'Organization settings' })).not.toHaveAttribute('aria-current')
  })

  it('opens the account menu from the sidebar footer, with instance administration for admins', async () => {
    renderSidebar('/app/acme/docs-public', { me: { ...me, user: { ...me.user, instance_admin: true } } })
    const account = await screen.findByRole('button', { name: 'Account' })
    expect(account.closest('[data-slot="sidebar-footer"]')).not.toBeNull()
    fireEvent.keyDown(account, { key: 'ArrowDown' })
    expect(await screen.findByRole('menuitem', { name: 'Account settings' })).toHaveAttribute('href', '/app/account/profile')
    expect(screen.getByRole('menuitem', { name: 'Instance administration' })).toHaveAttribute('href', '/app/admin/users')
    expect(screen.getByRole('menuitem', { name: 'Log out' })).toBeInTheDocument()
  })
})
