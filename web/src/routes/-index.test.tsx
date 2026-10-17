import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { Onboarding } from '@/features/sites/Onboarding'
import { landingTarget } from '@/features/sites/SitesOverviewLanding'

afterEach(cleanup)

const wrap = (node: ReactNode) => <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{node}</QueryClientProvider>

describe('onboarding', () => {
  it('defaults to the last visited site, or the first site, unless overview is chosen', () => {
    const single = {
      user: { id: 1, name: 'Owner', email: 'owner@example.com' },
      memberships: [{ org: { id: 1, name: 'Acme', slug: 'acme' }, role: 'owner' as const, sites: [{ id: 7, name: 'Docs', public_id: 'docs-public' }] }],
    }
    expect(landingTarget(single)).toBe('/app/acme/docs-public')
    expect(landingTarget({ ...single, memberships: [{ ...single.memberships[0], sites: [...single.memberships[0].sites, { id: 8, name: 'Shop', public_id: 'shop-public' }] }] })).toBe('/app/acme/docs-public')
  })

  it('honors overview and last-site preferences', () => {
    const me = {
      user: { id: 1, name: 'Owner', email: 'owner@example.com' },
      memberships: [{ org: { id: 1, name: 'Acme', slug: 'acme' }, role: 'owner' as const, sites: [{ id: 7, name: 'Docs', public_id: 'docs-public' }, { id: 8, name: 'Shop', public_id: 'shop-public' }] }],
    }
    expect(landingTarget({ ...me, preferences: { landing_page: 'overview', sites_view: 'cards' } })).toBeUndefined()
    expect(landingTarget({ ...me, preferences: { landing_page: 'last_site', sites_view: 'cards', last_site_id: 8 } })).toBe('/app/acme/shop-public')
  })

  it('creates a first site from a domain and opens Installation', async () => {
    const createSite = vi.fn().mockResolvedValue({
      id: 7,
      org_id: 1,
      public_id: 'site_public_123',
      name: 'Docs',
      timezone: 'Asia/Kolkata',
      retention_days: 30,
      allowed_hosts: ['docs.example.com'],
      revenue_currency: 'USD',
      revenue_currency_state: 'configured',
    })
    const navigate = vi.fn()
    render(wrap(
      <Onboarding
        me={{
          user: { id: 1, name: 'Owner', email: 'owner@example.com' },
          memberships: [
            {
              org: { id: 1, name: 'Acme', slug: 'acme' },
              role: 'owner',
              sites: [],
            },
          ],
        }}
        createSite={createSite}
        navigate={navigate}
      />,
    ))

    fireEvent.change(screen.getByLabelText('Domain'), {
      target: { value: 'docs.example.com' },
    })
    fireEvent.change(screen.getByLabelText('Raw data retention'), {
      target: { value: '90' },
    })
    fireEvent.change(screen.getByLabelText('Revenue currency (optional)'), {
      target: { value: 'usd' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Create site' }))

    await waitFor(() =>
      expect(createSite).toHaveBeenCalledWith({
        name: 'docs.example.com',
        hosts: ['docs.example.com'],
        timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC',
        retention_days: 90,
        revenue_currency: 'USD',
      }),
    )
    await waitFor(() =>
      expect(navigate).toHaveBeenCalledWith(
        '/app/acme/site_public_123/settings/installation',
      ),
    )
  })

  it('skips site creation and opens the organization empty state', () => {
    const navigate = vi.fn()
    render(wrap(
      <Onboarding
        me={{
          user: { id: 1, name: 'Owner', email: 'owner@example.com' },
          memberships: [
            {
              org: { id: 1, name: 'Acme', slug: 'acme' },
              role: 'owner',
              sites: [],
            },
          ],
        }}
        createSite={vi.fn()}
        navigate={navigate}
      />,
    ))

    fireEvent.click(screen.getByRole('button', { name: 'Skip for now' }))
    expect(navigate).toHaveBeenCalledWith('/app/acme/sites')
  })
})
