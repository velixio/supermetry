import { renderAuthorized as render } from '@/test/authorization'
import '@testing-library/jest-dom/vitest'

import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import { cleanup, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { ReplayLink } from './ReplayLink'

function renderLink(hasReplay: boolean) {
  const root = createRootRoute()
  const index = createRoute({
    getParentRoute: () => root,
    path: '/',
    component: () => <ReplayLink hasReplay={hasReplay} org="acme" site="shop" sessionId="42" />,
  })
  const router = createRouter({ routeTree: root.addChildren([index]), history: createMemoryHistory({ initialEntries: ['/'] }) })
  render(<RouterProvider router={router} />)
}

afterEach(cleanup)

describe('ReplayLink', () => {
  it('renders the play link only for rows with a replay', async () => {
    renderLink(false)
    expect(screen.queryByRole('link', { name: 'Play replay' })).not.toBeInTheDocument()
    cleanup()

    renderLink(true)
    expect((await screen.findByRole('link', { name: 'Play replay' })).getAttribute('href')).toContain('/acme/shop/replays/42')
  })
})
