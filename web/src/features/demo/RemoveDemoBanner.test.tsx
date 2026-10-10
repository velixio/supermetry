import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { deleteSite } = vi.hoisted(() => ({ deleteSite: vi.fn() }))
vi.mock('@/features/sites/api', () => ({ deleteSite }))

import { InstallationPage } from '@/features/settings/SettingsPages'
import type { Membership } from '@/routes/app.$org'
import { RemoveDemoBanner } from './RemoveDemoBanner'

const membership: Membership = {
  org: { id: 1, name: 'Acme', slug: 'acme' },
  role: 'owner',
  sites: [
    { id: 7, name: 'Demo project', public_id: 'demo-public', is_demo: true, demo_ready: true },
    { id: 8, name: 'Docs', public_id: 'docs-public', is_demo: false, demo_ready: false },
  ],
}

function renderBanner() {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false } } })}>
      <RemoveDemoBanner currentSite={membership.sites[1]} membership={membership} />
    </QueryClientProvider>,
  )
}

describe('RemoveDemoBanner', () => {
  beforeEach(() => {
    localStorage.clear()
    deleteSite.mockReset().mockResolvedValue(undefined)
  })
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('offers removal when a real project exists and Keep persists the choice', () => {
    renderBanner()
    expect(screen.getByText('Your project is ready. Remove the demo project?')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Keep' }))
    expect(screen.queryByText('Your project is ready. Remove the demo project?')).not.toBeInTheDocument()
    expect(localStorage.getItem('supermetry:demo-banner:1:demo-public')).toBe('keep')
  })



  it('shows again when the organization gets a new demo project', () => {
    const first = renderBanner()
    fireEvent.click(screen.getByRole('button', { name: 'Keep' }))
    first.unmount()

    const nextMembership: Membership = {
      ...membership,
      sites: [{ ...membership.sites[0], id: 9, public_id: 'new-demo-public' }, membership.sites[1]],
    }
    render(
      <QueryClientProvider client={new QueryClient()}>
        <RemoveDemoBanner currentSite={nextMembership.sites[1]} membership={nextMembership} />
      </QueryClientProvider>,
    )
    expect(screen.getByText('Your project is ready. Remove the demo project?')).toBeVisible()
  })

  it('still renders and hides when localStorage throws', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked') })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked') })
    renderBanner()
    expect(screen.getByText('Your project is ready. Remove the demo project?')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Keep' }))
    expect(screen.queryByText('Your project is ready. Remove the demo project?')).not.toBeInTheDocument()
  })

  it('removes the demo from the real-project banner', async () => {
    renderBanner()
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))
    await waitFor(() => expect(deleteSite).toHaveBeenCalledWith(7, 1))
  })

  it('hides tracker installation for demo projects', () => {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <InstallationPage orgId={1} siteId={7} publicId="demo-public" isDemo />
      </QueryClientProvider>,
    )
    expect(screen.getByText("Demo projects don't accept real traffic.")).toBeVisible()
    expect(screen.queryByRole('button', { name: /copy tracking snippet/i })).not.toBeInTheDocument()
  })
})
