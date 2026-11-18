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
  it('starts at the section on settings pages', async () => {
    await at('/app/acme/settings/api-keys')
    expect(screen.getByRole('link', { name: 'Settings' })).toBeInTheDocument()
    expect(screen.getByText('API keys')).toHaveAttribute('aria-current', 'page')
    expect(screen.queryByText('Acme')).toBeNull()
  })

  it('renders a current-page last crumb with no org or site ancestors', async () => {
    await at('/app/acme/docs/funnels')
    // Org and site live in the sidebar switcher; the trail names only the page.
    expect(screen.queryByRole('link', { name: 'Acme' })).toBeNull()
    expect(screen.queryByText('Docs')).toBeNull()
    expect(screen.getByText('Funnels')).toHaveAttribute('aria-current', 'page')
    // The current page is plain text with aria-current, never a link.
    expect(screen.queryByRole('link', { name: 'Funnels' })).toBeNull()
    expect(screen.getByText('Funnels').closest('a')).toBeNull()
  })

  it('marks all but the last two crumbs as hidden on small screens', async () => {
    await at('/app/acme/docs/links/pixel/9')
    const items = screen.getByRole('navigation', { name: 'Breadcrumb' }).querySelectorAll('li[data-crumb]')
    expect([...items].map((li) => li.className.includes('hidden'))).toEqual([true, false, false])
    const separators = screen.getByRole('navigation', { name: 'Breadcrumb' }).querySelectorAll('[data-slot="breadcrumb-separator"]')
    expect([...separators].map((li) => li.className.includes('hidden'))).toEqual([true, false])
  })

  it('renders nothing outside /app', async () => {
    const root = createRootRoute({ component: () => <Breadcrumbs membership={membership} /> })
    const router = createRouter({ routeTree: root, history: createMemoryHistory({ initialEntries: ['/login'] }) })
    const { container } = render(<RouterProvider router={router} />)
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(container.querySelector('nav')).toBeNull()
  })
})
