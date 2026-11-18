import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Outlet, RouterProvider, createMemoryHistory, createRootRoute, createRoute, createRouter } from '@tanstack/react-router'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { uiOnboarding } from '@/messages/uiOnboarding'

import { FeatureEmptyState, type EmptyKind } from './FeatureEmptyState'

async function renderAt(ui: React.ReactNode) {
  const root = createRootRoute({ component: Outlet })
  const page = createRoute({ getParentRoute: () => root, path: '/app/$org/$site/page', component: () => <>{ui}</> })
  const router = createRouter({ routeTree: root.addChildren([page]), history: createMemoryHistory({ initialEntries: ['/app/acme/example/page'] }) })
  await router.load()
  render(<QueryClientProvider client={new QueryClient()}><RouterProvider router={router} /></QueryClientProvider>)
}

const kinds = Object.keys(uiOnboarding.empty).filter((key) => key !== 'docs') as EmptyKind[]

describe('FeatureEmptyState', () => {
  afterEach(cleanup)

  it.each(kinds)('renders %s with a purpose, one action and a docs link', async (kind) => {
    await renderAt(<FeatureEmptyState kind={kind} />)
    const copy = uiOnboarding.empty[kind]
    expect(await screen.findByRole('heading', { name: copy.title })).toBeVisible()
    expect(screen.getByText(copy.purpose)).toBeVisible()
    expect(screen.getAllByRole('link', { name: copy.action })).toHaveLength(1)
    expect(screen.getByRole('link', { name: 'Read the docs' })).toHaveAttribute('href', 'https://supermetry.com/docs')
  })

  it('links the action to the settings page that turns the feature on', async () => {
    await renderAt(<FeatureEmptyState kind="heatmaps" />)
    expect(await screen.findByRole('link', { name: 'Turn on heatmaps' })).toHaveAttribute('href', '/app/acme/example/settings/tracking')
  })

  it('uses the page handler when the page can create the thing itself', async () => {
    const create = vi.fn()
    await renderAt(<FeatureEmptyState kind="funnels" onAction={create} />)
    expect(await screen.findByText('See where visitors drop off between steps.')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Create funnel' }))
    expect(create).toHaveBeenCalledOnce()
  })
})
