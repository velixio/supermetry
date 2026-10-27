import '@testing-library/jest-dom/vitest'

import { QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { Me } from '@/lib/me'
import { queryClient } from '@/lib/queryClient'
import { routeTree } from '@/routeTree.gen'

const me: Me = {
  user: { id: 1, name: 'Root', email: 'root@example.test', instance_admin: true },
  memberships: [{ org: { id: 1, name: 'Acme', slug: 'acme' }, role: 'owner', sites: [] }],
}

let requested: string[] = []

function routedFetch(managed: boolean) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const path = new URL(url, 'http://localhost').pathname
    requested.push(path)
    const json = path === '/api/private/me' ? me
      : path === '/api/private/auth/config' ? { signup: 'open', setup_required: false, homepage_url: 'https://supermetry.com', managed }
      : path === '/api/private/me/orgs/deleted' ? []
      : { data: [] }
    return new Response(JSON.stringify(json), { status: 200, headers: { 'Content-Type': 'application/json' } })
  })
}

function renderRoute(path: string, managed: boolean) {
  vi.stubGlobal('fetch', routedFetch(managed))
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: [path] }) })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return router
}

beforeEach(() => {
  requested = []
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

describe('managed instance', () => {
  it.each(['/app/admin/users', '/app/admin/update', '/app/admin'])('shows one calm notice at %s and calls no admin API', async (path) => {
    renderRoute(path, true)
    expect(await screen.findByRole('heading', { level: 1, name: 'This instance is managed by its operator.' })).toBeVisible()
    expect(screen.getByText('Instance administration is done outside the app, so there is nothing to configure here.')).toBeVisible()
    expect(screen.getByRole('link', { name: 'Back to the dashboard' })).toHaveAttribute('href', '/app')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: 'Instance administration sections' })).not.toBeInTheDocument()
    expect(requested.filter((p) => p.startsWith('/api/private/admin'))).toEqual([])
  })

  it('hides every entry that leads to the instance administration', async () => {
    renderRoute('/app/account/profile', true)
    const sidebar = await screen.findByRole('complementary', { name: 'Application sidebar' })
    await waitFor(() => expect(requested).toContain('/api/private/auth/config'))
    expect(within(sidebar).getByRole('link', { name: 'Account settings' })).toBeVisible()
    await waitFor(() => expect(within(sidebar).queryByRole('link', { name: 'Instance administration' })).not.toBeInTheDocument())
    expect(requested.filter((p) => p.startsWith('/api/private/admin'))).toEqual([])
  })

  it('keeps the instance administration for admins when the instance is not managed', async () => {
    renderRoute('/app/account/profile', false)
    const sidebar = await screen.findByRole('complementary', { name: 'Application sidebar' })
    expect(await within(sidebar).findByRole('link', { name: 'Instance administration' })).toHaveAttribute('href', '/app/admin/users')
  })

  it('shows the administration pages, not the notice, when not managed', async () => {
    renderRoute('/app/admin/users', false)
    expect(await screen.findByRole('navigation', { name: 'Instance administration sections' })).toBeVisible()
    expect(screen.queryByText('This instance is managed by its operator.')).not.toBeInTheDocument()
  })
})
