import '@testing-library/jest-dom/vitest'

import { QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, render, screen, within } from '@testing-library/react'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { Me, Membership } from '@/lib/me'
import { queryClient } from '@/lib/queryClient'
import { routeTree } from '@/routeTree.gen'

const acme: Membership = { org: { id: 1, name: 'Acme', slug: 'acme' }, role: 'owner', sites: [{ id: 7, name: 'Docs', public_id: 'docs-public' }] }
const me: Me = { user: { id: 1, name: 'Owner', email: 'owner@example.test' }, memberships: [acme] }

// /api/private/me answers with the given user; deleted organizations are an empty list; anything else an empty page.
function routedFetch(body: Me) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const path = new URL(url, 'http://localhost').pathname
    const json = path === '/api/private/me' ? body : path === '/api/private/me/orgs/deleted' ? [] : { data: [] }
    return new Response(JSON.stringify(json), { status: 200, headers: { 'Content-Type': 'application/json' } })
  })
}

function renderRoute(path: string, body: Me = me) {
  vi.stubGlobal('fetch', routedFetch(body))
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: [path] }) })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return router
}

beforeEach(() => {
  queryClient.clear()
  window.scrollTo = vi.fn()
  Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: 1440 })
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

afterEach(() => {
  cleanup()
  queryClient.clear()
  vi.unstubAllGlobals()
})

// The one shell: a 44px sticky header, the sidebar, and exactly one main landmark.
async function expectShell() {
  const header = await screen.findByRole('banner', { name: 'Application header' })
  expect(header).toHaveClass('sticky', 'top-0', 'h-11')
  expect(document.querySelectorAll('main')).toHaveLength(1)
  expect(screen.getByRole('main', { name: 'Main content' })).toHaveAttribute('id', 'main-content')
  return { header, sidebar: screen.getByRole('complementary', { name: 'Application sidebar' }) }
}

describe('one shell for every /app route', () => {
  it('renders account pages inside the shell with account breadcrumbs and links', async () => {
    renderRoute('/app/account/security')
    expect(await screen.findByRole('heading', { name: 'Account security' })).toBeVisible()
    const { header, sidebar } = await expectShell()
    const crumbs = within(header).getByRole('navigation', { name: 'Breadcrumb' })
    expect(within(crumbs).getByRole('link', { name: 'Account' })).toHaveAttribute('href', '/app/account')
    expect(within(crumbs).getByText('Security')).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('navigation', { name: 'Account settings' })).toBeVisible()
    expect(within(sidebar).getByRole('link', { name: 'Account settings' })).toHaveAttribute('aria-current', 'page')
    expect(within(sidebar).queryByRole('navigation', { name: 'Settings' })).toBeNull()
  })

  it('renders instance administration inside the shell for an instance admin', async () => {
    renderRoute('/app/admin/users', { ...me, user: { ...me.user, instance_admin: true } })
    expect(await screen.findByRole('navigation', { name: 'Instance administration sections' })).toBeVisible()
    const { sidebar } = await expectShell()
    expect(within(sidebar).getByRole('link', { name: 'Instance administration' })).toHaveAttribute('aria-current', 'page')
  })

  it('keeps the same shell mounted from account pages into an organization', async () => {
    const router = renderRoute('/app/account/security')
    await screen.findByRole('heading', { name: 'Account security' })
    const wrapper = document.querySelector('[data-slot="sidebar-wrapper"]')
    expect(wrapper).not.toBeNull()
    await act(async () => {
      await router.navigate({ to: '/app/$org', params: { org: 'acme' } })
    })
    expect(await screen.findByRole('navigation', { name: 'Settings' })).toBeInTheDocument()
    expect(document.querySelector('[data-slot="sidebar-wrapper"]')).toBe(wrapper)
    await expectShell()
  })

  it('renders an unknown organization as a page inside the shell', async () => {
    renderRoute('/app/missing')
    expect(await screen.findByText('We could not find that organisation')).toBeVisible()
    const { header } = await expectShell()
    expect(screen.getByRole('heading', { level: 1, name: 'Not found' })).toBeVisible()
    expect(within(header).queryByRole('navigation', { name: 'Breadcrumb' })).toBeNull()
    expect(screen.getByRole('link', { name: 'Acme' })).toHaveAttribute('href', '/app/acme/docs-public')
  })

  it('renders the no-organization screen inside the shell', async () => {
    renderRoute('/app', { ...me, memberships: [] })
    expect(await screen.findByRole('heading', { level: 1, name: "You don't belong to an organization" })).toBeVisible()
    const { sidebar } = await expectShell()
    expect(screen.getByRole('link', { name: 'Create organization' })).toHaveAttribute('href', '/app/account/organizations/new')
    expect(within(sidebar).getByRole('navigation', { name: 'Account' })).toBeInTheDocument()
  })

  it('renders first-site onboarding inside the shell', async () => {
    renderRoute('/app', { ...me, memberships: [{ ...acme, sites: [] }] })
    expect(await screen.findByRole('heading', { level: 1, name: 'What do you want to measure?' })).toBeVisible()
    await expectShell()
  })
})
