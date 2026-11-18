import '@testing-library/jest-dom/vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { AccountNotificationsPage } from './AccountNotificationsPage'
import { privateRequest } from './privateApi'

vi.mock('./privateApi', () => ({ privateRequest: vi.fn() }))
const request = vi.mocked(privateRequest)

const site = { site_id: 7, site_name: 'Desktop app', site_slug: 'desktop', org_slug: 'acme', org_name: 'Acme', assigned: true, resolved: false, regressed: true, weekly_digest: false }

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><AccountNotificationsPage /></QueryClientProvider>)
}

beforeEach(() => request.mockReset())
afterEach(cleanup)

describe('account notifications', () => {
  it('shows the defaults and saves only the changed switch for that project', async () => {
    request.mockImplementation((method) => Promise.resolve(method === 'GET' ? { sites: [site] } : { assigned: true, resolved: true, regressed: true, weekly_digest: false }))
    mount()
    expect(await screen.findByRole('switch', { name: 'Assigned to me' })).toBeChecked()
    expect(screen.getByRole('switch', { name: 'Regressed' })).toBeChecked()
    const resolved = screen.getByRole('switch', { name: 'Resolved' })
    expect(resolved).not.toBeChecked()
    fireEvent.click(resolved)
    await waitFor(() => expect(request).toHaveBeenCalledWith('PUT', '/api/private/me/app-notifications/7', {
      body: { assigned: true, resolved: true, regressed: true, weekly_digest: false },
    }))
  })

  it('says so when there are no app projects, and offers a retry on failure', async () => {
    request.mockResolvedValueOnce({ sites: [] })
    mount()
    expect(await screen.findByText('You have no app projects yet.')).toBeVisible()
    cleanup()
    request.mockReset()
    request.mockRejectedValueOnce(new Error('x')).mockResolvedValueOnce({ sites: [site] })
    mount()
    fireEvent.click(await screen.findByRole('button', { name: 'Try again' }))
    expect(await screen.findByText('Desktop app')).toBeVisible()
  })
})
