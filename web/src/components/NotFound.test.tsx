import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { InAppShell, NotFoundPage, RouteErrorPage, UnknownSite } from './NotFound'
import { renderWithRouter } from '@/test/renderWithRouter'
import type { Membership } from '@/lib/me'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })

describe('not-found and error pages', () => {
  it('gives an unknown URL a heading, a way home and the support link', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ support_url: 'https://help.example/contact' })))
    renderWithRouter(<NotFoundPage />)
    expect(await screen.findByRole('heading', { level: 1, name: 'Page not found' })).toBeVisible()
    expect(screen.getByRole('main')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Go to dashboard' })).toHaveAttribute('href', '/app')
    expect(await screen.findByRole('link', { name: 'Contact support' })).toHaveAttribute('href', 'https://help.example/contact')
  })

  it('hides the support link when none is configured and adds no second main inside the shell', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({})))
    renderWithRouter(<InAppShell.Provider value><NotFoundPage /></InAppShell.Provider>)
    expect(await screen.findByRole('heading', { name: 'Page not found' })).toBeVisible()
    expect(screen.queryByRole('main')).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Contact support' })).not.toBeInTheDocument()
  })

  it('never shows the raw error and offers a retry', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({})))
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const reset = vi.fn()
    renderWithRouter(<RouteErrorPage error={new Error('TypeError: x is undefined at chunk-123.js')} reset={reset} />)
    expect(await screen.findByRole('heading', { name: 'Something went wrong' })).toBeVisible()
    expect(screen.queryByText(/chunk-123/)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(reset).toHaveBeenCalled()
  })
})

describe('unknown site', () => {
  const membership: Membership = {
    org: { id: 1, name: 'Acme', slug: 'acme' },
    role: 'owner',
    sites: [{ id: 7, name: 'Docs', slug: 'docs', public_id: 'docs-public' }],
  }

  it('reloads the account once, then lists the sites the user can open', async () => {
    const fetchMock = vi.fn(async () => json({ user: { id: 1, name: 'O', email: 'o@example.test' }, memberships: [membership] }))
    vi.stubGlobal('fetch', fetchMock)
    renderWithRouter(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <UnknownSite membership={membership} />
      </QueryClientProvider>,
    )
    expect(await screen.findByRole('heading', { level: 1, name: 'Site not found' })).toBeVisible()
    expect(screen.getByRole('link', { name: 'Docs' })).toHaveAttribute('href', '/app/acme/docs')
    expect(screen.getByRole('link', { name: 'All sites' })).toHaveAttribute('href', '/app/acme/sites')
  })
})
