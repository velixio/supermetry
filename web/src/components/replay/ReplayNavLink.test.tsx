import '@testing-library/jest-dom/vitest'

import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { ReplayNavLink } from './ReplayNavLink'

function renderNav(enabled: boolean, props: Record<string, string> = {}) {
  const root = createRootRoute()
  const index = createRoute({
    getParentRoute: () => root,
    path: '/',
    component: () => <ReplayNavLink enabled={enabled} org="acme" site="shop" {...props} />,
  })
  const router = createRouter({ routeTree: root.addChildren([index]), history: createMemoryHistory({ initialEntries: ['/'] }) })
  render(<RouterProvider router={router} />)
}

afterEach(cleanup)

describe('ReplayNavLink', () => {
  it('is hidden when session replay is disabled for the instance', async () => {
    renderNav(false)
    expect(screen.queryByRole('link', { name: 'Replays' })).not.toBeInTheDocument()
    cleanup()

    renderNav(true)
    expect(await screen.findByRole('link', { name: 'Replays' })).toBeVisible()
  })

  it('keeps the classes and attributes the sidebar button passes through asChild', async () => {
    renderNav(true, { className: 'sidebar-button-classes', 'data-sidebar': 'menu-button' })
    const link = await screen.findByRole('link', { name: 'Replays' })
    expect(link).toHaveClass('sidebar-button-classes')
    expect(link).toHaveAttribute('data-sidebar', 'menu-button')
  })
})
