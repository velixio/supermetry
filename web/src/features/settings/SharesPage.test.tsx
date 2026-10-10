import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { SharesPage } from './SettingsPages'
import { privateRequest } from './privateApi'

vi.mock('./privateApi', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./privateApi')>()
  return { ...actual, privateRequest: vi.fn() }
})

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <SharesPage siteId={10} orgId={2} />
    </QueryClientProvider>,
  )
}

describe('share settings expiry', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.setSystemTime(new Date('2026-10-10T12:00:00Z'))
    vi.mocked(privateRequest).mockImplementation(async (method) => {
      if (method === 'GET') {
        return {
          data: [
            { id: 1, password_protected: false, expires_at: '2026-10-09T12:00:00Z' },
            { id: 2, password_protected: true, expires_at: '2026-11-09T12:00:00Z' },
          ],
        } as never
      }
      return { slug: 'shr_created' } as never
    })
  })

  afterEach(() => {
    cleanup()
    vi.useRealTimers()
    vi.clearAllMocks()
  })

  it('defaults new links to 90 days and submits the selected expiry', async () => {
    renderPage()

    expect(await screen.findByRole('combobox', { name: 'Expiry' })).toHaveTextContent('90 days')
    fireEvent.click(screen.getByRole('button', { name: 'Create link' }))

    await waitFor(() =>
      expect(privateRequest).toHaveBeenCalledWith(
        'POST',
        '/api/private/sites/10/shares',
        { orgId: 2, body: { password: '', expires_in_days: 90 } },
      ),
    )
  })

  it('shows expired and dated expiry badges', async () => {
    renderPage()

    expect(await screen.findByText('Expired')).toBeVisible()
    expect(screen.getByText('Expires Nov 9, 2026')).toBeVisible()
  })
})
