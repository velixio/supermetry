import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { Onboarding } from './app.index'

afterEach(cleanup)

describe('onboarding', () => {
  it('creates a first site with all settings and opens Installation', async () => {
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
    render(
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
    )

    fireEvent.change(screen.getByLabelText('Site name'), {
      target: { value: 'Docs' },
    })
    fireEvent.change(screen.getByLabelText('Domains'), {
      target: { value: 'docs.example.com' },
    })
    fireEvent.change(screen.getByLabelText('Time zone'), {
      target: { value: 'Asia/Kolkata' },
    })
    fireEvent.change(screen.getByLabelText('Retention'), {
      target: { value: '90' },
    })
    fireEvent.change(screen.getByLabelText('Revenue currency'), {
      target: { value: 'usd' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Create site' }))

    await waitFor(() =>
      expect(createSite).toHaveBeenCalledWith({
        name: 'Docs',
        hosts: ['docs.example.com'],
        timezone: 'Asia/Kolkata',
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
    render(
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
    )

    fireEvent.click(screen.getByRole('button', { name: 'Skip for now' }))
    expect(navigate).toHaveBeenCalledWith('/app/acme/sites')
  })
})
