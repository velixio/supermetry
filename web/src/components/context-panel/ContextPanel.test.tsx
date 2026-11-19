import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Outlet, RouterProvider, createMemoryHistory, createRootRoute, createRoute, createRouter } from '@tanstack/react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import type { Membership } from '@/lib/me'

import { ContextPanel, ContextPanelProvider, ContextPanelToggle } from '.'
import { readPanelOpen } from './storage'

const membership: Membership = { org: { id: 1, name: 'Acme', slug: 'acme' }, role: 'owner', sites: [{ id: 7, name: 'Docs', slug: 'docs' }] }

function setWidth(width: number) {
  Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: width })
}

function Shell() {
  return (
    <ContextPanelProvider userId={1} membership={membership}>
      <header><ContextPanelToggle /></header>
      <main><Outlet /></main>
      <ContextPanel membership={membership} />
    </ContextPanelProvider>
  )
}

function renderPanel(entry: string, seed?: (client: QueryClient) => void) {
  const root = createRootRoute({ component: Shell })
  const catchAll = createRoute({ getParentRoute: () => root, path: '$', component: () => <h1>Page</h1> })
  const router = createRouter({ routeTree: root.addChildren([catchAll]), history: createMemoryHistory({ initialEntries: [entry] }) })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  seed?.(client)
  render(
    <QueryClientProvider client={client}>
      <TooltipProvider><RouterProvider router={router} /></TooltipProvider>
    </QueryClientProvider>,
  )
  return router
}

describe('ContextPanel', () => {
  beforeEach(() => localStorage.clear())
  afterEach(() => {
    cleanup()
    setWidth(1024)
  })

  it('starts open on 1536px and wider as a complementary landmark after main, and closes with the toggle', async () => {
    setWidth(1600)
    renderPanel('/app/acme/docs/sessions?period=7d')
    const toggle = await screen.findByRole('button', { name: 'Page context' })
    const aside = await screen.findByRole('complementary', { name: 'Page context' })
    expect(toggle).toHaveAttribute('aria-pressed', 'true')
    expect(toggle).toHaveAttribute('aria-controls', aside.id)
    expect(screen.getByRole('main').compareDocumentPosition(aside) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    const related = within(aside).getByRole('region', { name: 'Related' })
    expect(within(related).getByRole('link', { name: 'Replays' })).toHaveAttribute('href', '/app/acme/docs/replays?period=7d')
    fireEvent.click(toggle)
    expect(screen.queryByRole('complementary')).not.toBeInTheDocument()
    expect(toggle).toHaveAttribute('aria-pressed', 'false')
    expect(readPanelOpen(1, 2000)).toBe(false)
  })

  it('stays closed below 1536px on first load, and remembers an explicit choice', async () => {
    setWidth(1400)
    renderPanel('/app/acme/docs/events')
    const toggle = await screen.findByRole('button', { name: 'Page context' })
    expect(screen.queryByRole('complementary')).not.toBeInTheDocument()
    fireEvent.click(toggle)
    expect(await screen.findByRole('complementary', { name: 'Page context' })).toBeInTheDocument()
    expect(readPanelOpen(1, 800)).toBe(true)
  })

  it('toggles with Ctrl or Cmd and the period key, and ignores other chords', async () => {
    setWidth(1600)
    renderPanel('/app/acme/docs/events')
    await screen.findByRole('complementary')
    fireEvent.keyDown(document, { key: '.', ctrlKey: true, shiftKey: true })
    fireEvent.keyDown(document, { key: ',', ctrlKey: true })
    expect(screen.getByRole('complementary')).toBeInTheDocument()
    fireEvent.keyDown(document, { key: '.', ctrlKey: true })
    expect(screen.queryByRole('complementary')).not.toBeInTheDocument()
    fireEvent.keyDown(document, { key: '.', metaKey: true })
    expect(await screen.findByRole('complementary')).toBeInTheDocument()
  })

  it('opens as a sheet below xl, moves focus in, and returns it to the toggle on close', async () => {
    setWidth(1024)
    renderPanel('/app/acme/docs/sessions')
    const toggle = await screen.findByRole('button', { name: 'Page context' })
    toggle.focus()
    fireEvent.click(toggle)
    const dialog = await screen.findByRole('dialog', { name: 'Page context' })
    expect(screen.queryByRole('complementary')).not.toBeInTheDocument()
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true))
    expect(toggle).toHaveAttribute('aria-pressed', 'true')
    fireEvent.keyDown(dialog, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await waitFor(() => expect(toggle).toHaveFocus())
    // The sheet never changes the remembered wide-screen choice.
    expect(readPanelOpen(1, 2000)).toBe(true)
  })

  it('closes the sheet after following a link', async () => {
    setWidth(1024)
    const router = renderPanel('/app/acme/docs/sessions')
    fireEvent.click(await screen.findByRole('button', { name: 'Page context' }))
    fireEvent.click(await screen.findByRole('link', { name: 'Events' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/app/acme/docs/events'))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('lists recent pages of the project and collapses a section, remembering it', async () => {
    setWidth(1600)
    const router = renderPanel('/app/acme/docs/events')
    await screen.findByRole('complementary')
    await router.navigate({ to: '/app/acme/docs/sessions' as never })
    const recent = await screen.findByRole('region', { name: 'Recent' })
    expect(within(recent).getByRole('link', { name: 'Events' })).toHaveAttribute('href', '/app/acme/docs/events')
    expect(within(recent).queryByRole('link', { name: 'Sessions' })).not.toBeInTheDocument()
    fireEvent.click(within(recent).getByRole('button', { name: 'Recent' }))
    expect(within(recent).getByRole('button', { name: 'Recent' })).toHaveAttribute('aria-expanded', 'false')
    expect(JSON.parse(localStorage.getItem('sm.contextPanel.sections.v1.1') ?? '[]')).toEqual(['recent'])
  })

  it('suggests actions from cached queries only', async () => {
    setWidth(1600)
    renderPanel('/app/acme/docs/sessions', (client) => {
      client.setQueryData(['tracking-status', 7], { install_status: 'waiting' })
      client.setQueryData(['replay', 7, 'settings'], { enabled: false })
      client.setQueryData(['data-health', 1], { drops_503: 3 })
    })
    const suggested = await screen.findByRole('region', { name: 'Suggested' })
    expect(within(suggested).getAllByRole('link').map((link) => link.textContent)).toEqual(['Install the tracker', 'Review rejected events', 'Turn on recording'])
    expect(within(suggested).getByRole('link', { name: 'Turn on recording' })).toHaveAttribute('href', '/app/acme/docs/settings/replay')
  })

  it('shows sidebar pins and saved dashboards under quick access', async () => {
    setWidth(1600)
    localStorage.setItem('sm.sidebar.v1.1', JSON.stringify({ open: {}, pinned: ['nav:/retention'] }))
    renderPanel('/app/acme/docs/sessions', (client) => client.setQueryData(['dashboards', 7], [{ id: 0, name: 'Built-in' }, { id: 5, name: 'Growth' }]))
    const quick = await screen.findByRole('region', { name: 'Quick access' })
    expect(within(quick).getByRole('link', { name: 'Retention' })).toHaveAttribute('href', '/app/acme/docs/retention')
    expect(within(quick).getByRole('link', { name: 'Dashboard: Growth' })).toHaveAttribute('href', '/app/acme/docs?dash=5')
    expect(within(quick).queryByRole('link', { name: /Built-in/ })).not.toBeInTheDocument()
  })

  it('renders no toggle without a provider', async () => {
    render(<TooltipProvider><ContextPanelToggle /></TooltipProvider>)
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })
})
