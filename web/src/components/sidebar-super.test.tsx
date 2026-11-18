import '@testing-library/jest-dom/vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { Outlet, RouterProvider, createMemoryHistory, createRootRoute, createRoute, createRouter } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { liveSummaryKey } from '@/features/realtime/liveSummaryKey'
import type { Me, Membership } from '@/lib/me'

import { SupermetrySidebar } from './Sidebar'
import { SidebarProvider } from './ui/sidebar'
import { TooltipProvider } from './ui/tooltip'

const membership: Membership = {
  org: { id: 1, name: 'Acme', slug: 'acme' },
  role: 'owner',
  effective_capabilities: ['profiles.read', 'replay.read'],
  sites: [{ id: 1, name: 'Docs', public_id: 'docs-public', tracking_policy: { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }],
}
const me: Me = { user: { id: 1, name: 'Owner', email: 'owner@supermetry.test' }, memberships: [membership] }

function renderSidebar(initialEntry = '/app/acme/docs-public/realtime', options: { defaultOpen?: boolean; client?: QueryClient } = {}) {
  localStorage.clear()
  const client = options.client ?? new QueryClient()
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={client}>
        <TooltipProvider>
          <SidebarProvider defaultOpen={options.defaultOpen ?? true}>
            <SupermetrySidebar me={me} membership={membership} requestedSite="docs-public" />
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
  return { router, client }
}

const wrapper = () => document.querySelector('[data-slot="sidebar"]') as HTMLElement
const container = () => document.querySelector('[data-slot="sidebar-container"]') as HTMLElement
const press = (key: string, target: Element = document.body) => fireEvent.keyDown(target, { key })

beforeEach(() => {
  globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} }
  window.scrollTo = () => undefined
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: () => ({ matches: false, addEventListener: () => undefined, removeEventListener: () => undefined }),
  })
})
afterEach(() => { vi.useRealTimers(); cleanup(); localStorage.clear() })

describe('peek', () => {
  it('slides the full sidebar over the content after 300ms of hover and closes 200ms after the pointer leaves', async () => {
    renderSidebar('/app/acme/docs-public/realtime', { defaultOpen: false })
    await screen.findByRole('link', { name: 'Realtime' })
    expect(wrapper()).toHaveAttribute('data-collapsible', 'icon')
    vi.useFakeTimers()
    fireEvent.pointerEnter(container(), { pointerType: 'mouse' })
    act(() => { vi.advanceTimersByTime(299) })
    expect(wrapper()).not.toHaveAttribute('data-peek')
    act(() => { vi.advanceTimersByTime(1) })
    expect(wrapper()).toHaveAttribute('data-peek', 'true')
    // Full width, drawn over the content: the gap keeps the rail footprint, the sidebar state stays collapsed.
    expect(wrapper()).toHaveAttribute('data-collapsible', '')
    expect(wrapper()).toHaveAttribute('data-state', 'collapsed')
    expect(container()).toHaveClass('group-data-[peek=true]:shadow-md', 'motion-reduce:transition-none')
    expect(document.querySelector('[data-slot="sidebar-gap"]')).toHaveClass('group-data-[peek=true]:w-(--sidebar-width-icon)')
    fireEvent.pointerLeave(container(), { pointerType: 'mouse' })
    act(() => { vi.advanceTimersByTime(199) })
    expect(wrapper()).toHaveAttribute('data-peek', 'true')
    act(() => { vi.advanceTimersByTime(1) })
    expect(wrapper()).not.toHaveAttribute('data-peek')
  })

  it('does not open for a pointer that leaves early, nor for touch', async () => {
    renderSidebar('/app/acme/docs-public/realtime', { defaultOpen: false })
    await screen.findByRole('link', { name: 'Realtime' })
    vi.useFakeTimers()
    fireEvent.pointerEnter(container(), { pointerType: 'mouse' })
    act(() => { vi.advanceTimersByTime(200) })
    fireEvent.pointerLeave(container(), { pointerType: 'mouse' })
    act(() => { vi.advanceTimersByTime(1000) })
    expect(wrapper()).not.toHaveAttribute('data-peek')
    fireEvent.pointerEnter(container(), { pointerType: 'touch' })
    act(() => { vi.advanceTimersByTime(1000) })
    expect(wrapper()).not.toHaveAttribute('data-peek')
  })

  it('closes on Escape, and Ctrl+B pins the sidebar open', async () => {
    renderSidebar('/app/acme/docs-public/realtime', { defaultOpen: false })
    await screen.findByRole('link', { name: 'Realtime' })
    vi.useFakeTimers()
    fireEvent.pointerEnter(container(), { pointerType: 'mouse' })
    act(() => { vi.advanceTimersByTime(300) })
    expect(wrapper()).toHaveAttribute('data-peek', 'true')
    fireEvent.keyDown(container(), { key: 'Escape' })
    expect(wrapper()).not.toHaveAttribute('data-peek')
    fireEvent.pointerEnter(container(), { pointerType: 'mouse' })
    act(() => { vi.advanceTimersByTime(300) })
    fireEvent.keyDown(window, { key: 'b', ctrlKey: true })
    expect(wrapper()).toHaveAttribute('data-state', 'expanded')
    expect(wrapper()).not.toHaveAttribute('data-peek')
  })

  it('shows the settings sub-pages while peeking instead of needing a flyout', async () => {
    renderSidebar('/app/acme/docs-public/settings/tracking', { defaultOpen: false })
    await screen.findByRole('link', { name: 'Realtime' })
    vi.useFakeTimers()
    fireEvent.pointerEnter(container(), { pointerType: 'mouse' })
    act(() => { vi.advanceTimersByTime(300) })
    const nav = screen.getByRole('navigation', { name: 'Settings' })
    expect(within(nav).getByRole('link', { name: 'Tracking' })).toHaveAttribute('aria-current', 'page')
    // The rail's filter button became the field.
    expect(screen.getByRole('searchbox', { name: 'Filter menu' })).toBeInTheDocument()
  })
})

describe('icon rail', () => {
  it('shows only the project avatar in the switcher, with a project tooltip', async () => {
    renderSidebar('/app/acme/docs-public/realtime', { defaultOpen: false })
    const trigger = await screen.findByRole('button', { name: /Docs\s*Acme/ })
    expect(trigger.querySelector('[data-scope="site"]')).toHaveClass('group-data-[collapsible=icon]:hidden')
    expect(trigger).toHaveClass('group-data-[collapsible=icon]:justify-center', 'group-data-[collapsible=icon]:p-0!')
    fireEvent.focus(trigger)
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Project: Docs · Acme')
  })

  it('names the account button with the user name in its tooltip and keeps it centered', async () => {
    renderSidebar('/app/acme/docs-public/realtime', { defaultOpen: false })
    const account = await screen.findByRole('button', { name: 'Account' })
    expect(account).toHaveClass('group-data-[collapsible=icon]:justify-center')
    fireEvent.focus(account)
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Owner')
  })

  it('shows the go-to chord in a rail tooltip', async () => {
    renderSidebar('/app/acme/docs-public/realtime', { defaultOpen: false })
    fireEvent.focus(await screen.findByRole('link', { name: 'Funnels' }))
    expect(await screen.findByRole('tooltip')).toHaveTextContent('FunnelsG F')
  })

  it('separates the groups with a hairline on the rail', async () => {
    renderSidebar('/app/acme/docs-public/realtime', { defaultOpen: false })
    await screen.findByRole('link', { name: 'Realtime' })
    const groups = document.querySelectorAll('[data-slot="sidebar-group"]')
    expect(groups[1]).toHaveClass('group-data-[collapsible=icon]:not-first:border-t')
  })
})

describe('go-to chords', () => {
  beforeEach(() => { vi.useFakeTimers({ toFake: ['Date'] }) })

  it('goes to a page on g then a letter, including settings with a comma', async () => {
    vi.useRealTimers()
    const { router } = renderSidebar()
    await screen.findByRole('link', { name: 'Realtime' })
    vi.useFakeTimers({ toFake: ['Date'] })
    press('g')
    press('e')
    await vi.waitFor(() => expect(router.state.location.pathname).toBe('/app/acme/docs-public/events'))
    press('g')
    press(',')
    await vi.waitFor(() => expect(router.state.location.pathname).toBe('/app/acme/docs-public/settings/general'))
    press('g')
    press('v')
    await vi.waitFor(() => expect(router.state.location.pathname).toBe('/app/acme/docs-public/revenue'))
  })

  it('ignores the chord after a second, with another second key, in fields and in dialogs', async () => {
    vi.useRealTimers()
    const { router } = renderSidebar()
    await screen.findByRole('link', { name: 'Realtime' })
    vi.useFakeTimers({ toFake: ['Date'] })
    const here = router.state.location.pathname
    press('g')
    vi.setSystemTime(Date.now() + 1100)
    press('e')
    press('g')
    press('x')
    press('e')
    const outside = screen.getByRole('textbox', { name: 'Outside field' })
    press('g', outside)
    press('e', outside)
    const dialog = document.createElement('div')
    dialog.setAttribute('role', 'dialog')
    document.body.append(dialog)
    press('g')
    press('e')
    dialog.remove()
    await new Promise((resolve) => setTimeout(resolve, 20))
    expect(router.state.location.pathname).toBe(here)
  })

  it('does not turn Ctrl+G into a chord', async () => {
    vi.useRealTimers()
    const { router } = renderSidebar()
    await screen.findByRole('link', { name: 'Realtime' })
    const here = router.state.location.pathname
    fireEvent.keyDown(document.body, { key: 'g', ctrlKey: true })
    press('e')
    await new Promise((resolve) => setTimeout(resolve, 20))
    expect(router.state.location.pathname).toBe(here)
  })

  it('shows the chord as CSS-drawn text that stays out of the row text and name', async () => {
    renderSidebar()
    const link = await screen.findByRole('link', { name: 'Events' })
    expect(link.textContent).toBe('Events')
    expect(link.querySelector('[data-chord="G E"]')).toHaveAttribute('aria-hidden', 'true')
  })
})

describe('roving keyboard navigation', () => {
  it('keeps one item in the tab order and moves with arrows, Home and End', async () => {
    renderSidebar('/app/acme/docs-public/realtime', { })
    await screen.findByRole('link', { name: 'Realtime' })
    const items = () => Array.from(document.querySelectorAll<HTMLElement>('[data-nav-item]'))
    expect(items().filter((el) => el.tabIndex === 0)).toHaveLength(1)
    const realtime = screen.getByRole('link', { name: 'Realtime' })
    expect(realtime.tabIndex).toBe(0)
    realtime.focus()
    fireEvent.keyDown(realtime, { key: 'ArrowDown' })
    expect(document.activeElement).toBe(screen.getByRole('link', { name: 'Replays' }))
    expect((document.activeElement as HTMLElement).tabIndex).toBe(0)
    expect(realtime.tabIndex).toBe(-1)
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowUp' })
    expect(document.activeElement).toBe(realtime)
    fireEvent.keyDown(realtime, { key: 'Home' })
    expect(document.activeElement).toHaveTextContent('Analyze')
    fireEvent.keyDown(document.activeElement!, { key: 'End' })
    expect(document.activeElement).toBe(screen.getByRole('link', { name: 'Organization settings' }))
  })

  it('skips collapsed groups, collapses with ArrowLeft and expands with ArrowRight on headings', async () => {
    renderSidebar('/app/acme/docs-public/realtime')
    await screen.findByRole('link', { name: 'Realtime' })
    const heading = screen.getByRole('button', { name: 'Behavior' })
    heading.focus()
    fireEvent.keyDown(heading, { key: 'ArrowLeft' })
    expect(heading).toHaveAttribute('aria-expanded', 'false')
    fireEvent.keyDown(heading, { key: 'ArrowDown' })
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Conversion' }))
    fireEvent.keyDown(heading, { key: 'ArrowRight' })
    expect(heading).toHaveAttribute('aria-expanded', 'true')
    fireEvent.keyDown(heading, { key: 'ArrowRight' })
    expect(document.activeElement).toBe(screen.getByRole('link', { name: 'Journeys' }))
    // ArrowLeft on a row goes up to its group heading.
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowLeft' })
    expect(document.activeElement).toBe(heading)
  })

  it('expands a nested item with ArrowRight, enters it, and ArrowLeft returns to the parent', async () => {
    renderSidebar('/app/acme/docs-public/realtime')
    await screen.findByRole('link', { name: 'Realtime' })
    const parent = screen.getByRole('link', { name: 'Project settings' })
    parent.focus()
    fireEvent.keyDown(parent, { key: 'ArrowRight' })
    expect(screen.getByRole('button', { name: 'Collapse Project settings' })).toBeInTheDocument()
    fireEvent.keyDown(parent, { key: 'ArrowRight' })
    expect(document.activeElement).toBe(screen.getByRole('link', { name: 'General' }))
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowLeft' })
    expect(document.activeElement).toBe(parent)
    fireEvent.keyDown(parent, { key: 'ArrowLeft' })
    expect(screen.getByRole('button', { name: 'Expand Project settings' })).toBeInTheDocument()
  })

  it('opens the peek on ArrowRight in the rail', async () => {
    renderSidebar('/app/acme/docs-public/realtime', { defaultOpen: false })
    await screen.findByRole('link', { name: 'Realtime' })
    const parent = screen.getByRole('link', { name: 'Project settings' })
    parent.focus()
    fireEvent.keyDown(parent, { key: 'ArrowRight' })
    expect(wrapper()).toHaveAttribute('data-peek', 'true')
  })

  it('pins a row with Shift+P', async () => {
    renderSidebar('/app/acme/docs-public/realtime')
    const link = await screen.findByRole('link', { name: 'Goals' })
    link.focus()
    fireEvent.keyDown(link, { key: 'P', shiftKey: true })
    expect(JSON.parse(localStorage.getItem('sm.sidebar.v1.1') ?? '{}').pinned).toContain('nav:/goals')
  })
})

describe('live dot', () => {
  it('shows a dot on Realtime from the cached live summary and fetches nothing itself', async () => {
    const client = new QueryClient()
    const fetcher = vi.spyOn(globalThis, 'fetch')
    renderSidebar('/app/acme/docs-public', { client })
    const link = await screen.findByRole('link', { name: 'Realtime' })
    expect(screen.queryByTestId('live-dot')).toBeNull()
    act(() => { client.setQueryData(liveSummaryKey(1), { active_visitors: 3, visitors_last_30m: 9 }) })
    expect(await screen.findByTestId('live-dot', undefined, { timeout: 4000 })).toHaveAttribute('aria-hidden', 'true')
    expect(link).toHaveAttribute('aria-description', '3 live now')
    expect(link.textContent).toBe('Realtime')
    act(() => { client.setQueryData(liveSummaryKey(1), { active_visitors: 0, visitors_last_30m: 9 }) })
    await waitFor(() => expect(screen.queryByTestId('live-dot')).toBeNull(), { timeout: 4000 })
    expect(fetcher).not.toHaveBeenCalled()
    fetcher.mockRestore()
  })
})
