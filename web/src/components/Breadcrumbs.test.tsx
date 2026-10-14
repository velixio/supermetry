import '@testing-library/jest-dom/vitest'

import { RouterProvider, createMemoryHistory, createRootRoute, createRouter } from '@tanstack/react-router'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import type { Membership } from '@/lib/me'

import { Breadcrumbs } from './Breadcrumbs'

afterEach(cleanup)

const membership: Membership = {
  org: { id: 1, name: 'Acme', slug: 'acme' },
  role: 'owner',
  sites: [{ id: 7, name: 'Docs', slug: 'docs' }],
}

async function at(path: string) {
  const root = createRootRoute({ component: () => <Breadcrumbs membership={membership} /> })
  const router = createRouter({ routeTree: root, history: createMemoryHistory({ initialEntries: [path] }) })
  render(<RouterProvider router={router} />)
  await screen.findByRole('navigation', { name: 'Breadcrumb' })
}

describe('Breadcrumbs', () => {
  it('renders linked ancestors and a current-page last crumb', async () => {
    await at('/app/acme/docs/funnels')
    expect(screen.getByRole('link', { name: 'Acme' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Docs' })).toBeInTheDocument()
    expect(screen.getByText('Funnels')).toHaveAttribute('aria-current', 'page')
    // The current page is plain text with aria-current, never a link.
    expect(screen.queryByRole('link', { name: 'Funnels' })).toBeNull()
    expect(screen.getByText('Funnels').closest('a')).toBeNull()
  })

  it('marks all but the last two crumbs as hidden on small screens', async () => {
    await at('/app/acme/docs/funnels/42')
    const items = screen.getByRole('navigation', { name: 'Breadcrumb' }).querySelectorAll('li[data-crumb]')
    expect([...items].map((li) => li.className.includes('hidden'))).toEqual([true, true, false, false])
    const separators = screen.getByRole('navigation', { name: 'Breadcrumb' }).querySelectorAll('[data-slot="breadcrumb-separator"]')
    expect([...separators].map((li) => li.className.includes('hidden'))).toEqual([true, true, false])
  })

  it('renders nothing outside /app', async () => {
    const root = createRootRoute({ component: () => <Breadcrumbs membership={membership} /> })
    const router = createRouter({ routeTree: root, history: createMemoryHistory({ initialEntries: ['/login'] }) })
    const { container } = render(<RouterProvider router={router} />)
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(container.querySelector('nav')).toBeNull()
  })
})
