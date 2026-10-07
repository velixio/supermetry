import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { Onboarding } from './index'

afterEach(cleanup)

describe('onboarding', () => {
  it('moves from first-site creation to the tracking snippet', async () => {
    const createSite = vi.fn().mockResolvedValue({
      id: 7,
      org_id: 1,
      public_id: 'site_public_123',
      name: 'Docs',
      timezone: 'Asia/Kolkata',
      retention_days: 30,
      allowed_hosts: ['docs.example.com'],
    })
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
      />,
    )

    fireEvent.change(screen.getByLabelText('Site name'), {
      target: { value: 'Docs' },
    })
    fireEvent.change(screen.getByLabelText('Website host'), {
      target: { value: 'docs.example.com' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Create site' }))

    await waitFor(() =>
      expect(createSite).toHaveBeenCalledWith({
        name: 'Docs',
        hosts: ['docs.example.com'],
        timezone: expect.any(String),
        retention_days: 30,
      }),
    )
    expect(await screen.findByText('Add the tracking snippet')).toBeVisible()
    expect(screen.getByText(/site_public_123/)).toBeVisible()
    expect(screen.getByText('Waiting for the first event…')).toBeVisible()
  })
})
