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
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    Element.prototype.scrollIntoView = vi.fn()
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
    vi.unstubAllGlobals()
  })

  it('defaults new links to 90 days and submits the selected expiry', async () => {
    renderPage()

    expect(await screen.findByRole('combobox', { name: 'Expiry' })).toHaveTextContent('90 days')
    fireEvent.click(screen.getByRole('button', { name: 'Create link' }))

    await waitFor(() =>
      expect(privateRequest).toHaveBeenCalledWith(
        'POST',
        '/api/private/sites/10/shares',
        { orgId: 2, body: expect.objectContaining({ password: '', expires_in_days: 90, config: expect.any(Object) }) },
      ),
    )
  })

  it('submits visible sections, locked scope, theme, date and confirmed embed policy', async () => {
    renderPage()

    await screen.findByRole('heading', { name: 'Share preview' })
    fireEvent.click(screen.getByRole('checkbox', { name: 'Sources' }))
    fireEvent.change(screen.getByLabelText('Locked country'), { target: { value: 'IN' } })
    fireEvent.click(screen.getByRole('combobox', { name: 'Theme' }))
    fireEvent.click(screen.getByRole('option', { name: 'Dark' }))
    fireEvent.click(screen.getByRole('switch', { name: 'Allow embedding' }))
    fireEvent.click(screen.getByRole('checkbox', { name: /allow embedding on any origin/i }))
    fireEvent.click(screen.getByRole('button', { name: 'Create link' }))

    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith(
      'POST',
      '/api/private/sites/10/shares',
      { orgId: 2, body: expect.objectContaining({
        config: expect.objectContaining({
          sections: expect.not.arrayContaining(['sources']),
          filters: [{ dimension: 'country', operator: 'is', values: ['IN'] }],
          theme: 'dark',
          embed: { enabled: true, allowed_origins: [], confirm_wildcard: true },
        }),
      }) },
    ))
    expect(await screen.findByRole('button', { name: 'Copy embed code' })).toBeVisible()
  })

  it('submits a saved segment as the locked scope', async () => {
    vi.mocked(privateRequest).mockImplementation(async (method, path) => {
      if (method === 'GET' && path.endsWith('/segments')) return [{ id: 17, name: 'Paid visitors' }] as never
      if (method === 'GET') return { data: [] } as never
      return { slug: 'shr_segment' } as never
    })
    renderPage()

    fireEvent.click(await screen.findByRole('combobox', { name: 'Saved segment' }))
    fireEvent.click(screen.getByRole('option', { name: 'Paid visitors' }))
    fireEvent.click(screen.getByRole('button', { name: 'Create link' }))

    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith(
      'POST',
      '/api/private/sites/10/shares',
      { orgId: 2, body: expect.objectContaining({
        config: expect.objectContaining({ segment_id: 17, filters: [] }),
      }) },
    ))
  })

  it('shows expired and dated expiry badges', async () => {
    renderPage()

    expect(await screen.findByText('Expired')).toBeVisible()
    expect(screen.getByText('Expires Nov 9, 2026')).toBeVisible()
  })
})
