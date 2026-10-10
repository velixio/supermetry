import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { createDemo, dismissDemoPrompt } = vi.hoisted(() => ({
  createDemo: vi.fn(),
  dismissDemoPrompt: vi.fn(),
}))

vi.mock('./api', () => ({ createDemo, dismissDemoPrompt }))

import { SidebarProvider } from '@/components/ui/sidebar'
import { TooltipProvider } from '@/components/ui/tooltip'
import { SupermetrySidebar } from '@/components/Sidebar'
import { Onboarding } from '@/routes/app.index'
import type { Me, Membership } from '@/routes/app.$org'

const organization = { id: 1, name: 'Acme', slug: 'acme' }

function ownerMe(overrides: Partial<Me> = {}): Me {
  return {
    user: { id: 1, name: 'Owner', email: 'owner@example.com' },
    memberships: [{ org: organization, role: 'owner', sites: [] }],
    demo_prompt_dismissed: false,
    ...overrides,
  }
}

function renderOnboarding(me = ownerMe()) {
  const root = createRootRoute({ component: Outlet })
  const app = createRoute({
    getParentRoute: () => root,
    path: 'app',
    component: () => <><Onboarding me={me} createSite={vi.fn()} navigate={vi.fn()} /><Outlet /></>,
  })
  const org = createRoute({ getParentRoute: () => app, path: '$org', component: Outlet })
  const site = createRoute({ getParentRoute: () => org, path: '$site', component: () => <p>Demo dashboard</p> })
  const router = createRouter({
    routeTree: root.addChildren([app.addChildren([org.addChildren([site])])]),
    history: createMemoryHistory({ initialEntries: ['/app'] }),
  })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const view = render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)
  return { router, ...view }
}

describe('demo onboarding and project switcher', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    window.scrollTo = () => undefined
    createDemo.mockResolvedValue({ public_id: 'demo-public' })
    dismissDemoPrompt.mockResolvedValue(undefined)
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: () => ({ matches: false, addEventListener: () => undefined, removeEventListener: () => undefined }),
    })
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
      unobserve() {}
      disconnect() {}
    })
  })

  afterEach(cleanup)

  it('creates demo data from onboarding and navigates to the demo project', async () => {
    const { router } = renderOnboarding()

    fireEvent.click(await screen.findByRole('button', { name: 'Explore with demo data' }))

    await waitFor(() => expect(createDemo).toHaveBeenCalledWith(1, expect.any(String)))
    await waitFor(() => expect(router.state.location.pathname).toBe('/app/acme/demo-public'))
  })

  it('dismisses the onboarding choice and hides an already dismissed prompt', async () => {
    renderOnboarding()
    fireEvent.click(await screen.findByRole('button', { name: 'Skip for now' }))
    await waitFor(() => expect(dismissDemoPrompt).toHaveBeenCalledOnce())

    cleanup()
    renderOnboarding(ownerMe({ demo_prompt_dismissed: true }))
    expect(screen.queryByRole('button', { name: 'Explore with demo data' })).not.toBeInTheDocument()
  })

  it('offers one demo project in the switcher and labels the demo row', async () => {
    const membership: Membership = {
      org: organization,
      role: 'owner',
      sites: [
        { id: 7, name: 'Demo project', public_id: 'demo-public', is_demo: true, demo_ready: true },
        { id: 8, name: 'Docs', public_id: 'docs-public', is_demo: false, demo_ready: false },
      ],
    }
    const me = ownerMe({ memberships: [membership] })
    const root = createRootRoute({ component: Outlet })
    const app = createRoute({ getParentRoute: () => root, path: 'app', component: () => <QueryClientProvider client={new QueryClient()}><TooltipProvider><SidebarProvider><SupermetrySidebar me={me} membership={membership} requestedSite="docs-public" /></SidebarProvider></TooltipProvider></QueryClientProvider> })
    const router = createRouter({ routeTree: root.addChildren([app]), history: createMemoryHistory({ initialEntries: ['/app'] }) })
    const view = render(<RouterProvider router={router} />)

    fireEvent.pointerDown(await screen.findByRole('button', { name: /Docs/ }), { button: 0, ctrlKey: false })
    expect(await screen.findByText('Demo', { selector: '[data-slot="badge"]' })).toBeVisible()
    expect(screen.queryByRole('menuitem', { name: 'Add demo project' })).not.toBeInTheDocument()

    view.unmount()
    const noDemo = { ...membership, sites: membership.sites.filter((site) => !site.is_demo) }
    const noDemoMe = ownerMe({ memberships: [noDemo] })
    const nextRoot = createRootRoute({ component: Outlet })
    const nextApp = createRoute({ getParentRoute: () => nextRoot, path: 'app', component: () => <QueryClientProvider client={new QueryClient()}><TooltipProvider><SidebarProvider><SupermetrySidebar me={noDemoMe} membership={noDemo} requestedSite="docs-public" /></SidebarProvider></TooltipProvider></QueryClientProvider> })
    const nextRouter = createRouter({ routeTree: nextRoot.addChildren([nextApp]), history: createMemoryHistory({ initialEntries: ['/app'] }) })
    render(<RouterProvider router={nextRouter} />)
    fireEvent.pointerDown(await screen.findByRole('button', { name: /Docs/ }), { button: 0, ctrlKey: false })
    expect(await screen.findByRole('menuitem', { name: 'Add demo project' })).toBeVisible()
  })
})
