import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { AccountSessionsPage } from './AccountSessionsPage'
import { privateRequest } from './privateApi'

vi.mock('./privateApi', () => ({ privateRequest: vi.fn() }))
const request = vi.mocked(privateRequest)
const created = '2026-10-08T09:10:11.123Z'
const active = '2026-10-09T10:20:30Z'
const current = { ID: 'current', Browser: 'Firefox', IPAddress: '2001:db8::1', CountryCode: 'DE', CreatedAt: created, LastSeenAt: active, Current: true }
const other = { ID: 'other/id', Browser: 'Chrome', IPAddress: '192.0.2.1', CountryCode: 'US', CreatedAt: created, LastSeenAt: '2026-10-09T11:00:00Z' }

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><AccountSessionsPage /></QueryClientProvider>)
}

beforeEach(() => request.mockReset())
afterEach(cleanup)

describe('account active sessions', () => {
  it('shows current first, metadata and same-origin decorative icons with accessible country text', async () => {
    request.mockResolvedValue({ data: [other, current] })
    mount()
    await screen.findByText('Firefox')
    const rows = within(screen.getByRole('list', { name: 'Active account sessions' })).getAllByRole('listitem')
    expect(within(rows[0]).getByText('Current session')).toBeVisible()
    expect(within(rows[0]).getByText('2001:db8::1')).toBeVisible()
    expect(within(rows[0]).getByText('Germany (DE)')).toBeVisible()
    expect(within(rows[1]).getByText('United States (US)')).toBeVisible()
    for (const img of document.querySelectorAll('img')) {
      expect(img.getAttribute('src')).toMatch(/^\/(icons|flags)\//)
      expect(img).toHaveAttribute('alt', '')
      expect(img).toHaveAttribute('aria-hidden', 'true')
    }
    expect(rows[0].querySelector('dl')).toHaveClass('grid-cols-1', 'sm:grid-cols-2')
    expect(within(rows[0]).getByText('2001:db8::1')).toHaveClass('break-all')
  })

  it('preserves distinct device labels as wrapped secondary text without empty or duplicate labels', async () => {
    request.mockResolvedValue({ data: [
      { ...current, DeviceLabel: '  Work laptop  ' },
      { ...other, DeviceLabel: ' chrome ' },
      { ID: 'legacy-label', DeviceLabel: 'Personal tablet' },
      { ID: 'blank-label', DeviceLabel: '   ' },
    ] })
    mount()
    expect(await screen.findByText('Work laptop')).toHaveClass('break-words', 'text-muted-foreground')
    expect(screen.getByText('Personal tablet')).toBeVisible()
    expect(screen.queryByText('chrome', { exact: true })).not.toBeInTheDocument()
    const rows = screen.getAllByRole('listitem')
    expect(rows[1].querySelector('p')).toBeNull()
    expect(rows[3].querySelector('p')).toBeNull()
  })

  it('uses local timezone with exact machine-readable and accessible timestamps', async () => {
    request.mockResolvedValue({ data: [current] })
    mount()
    await screen.findByText('Firefox')
    const date = new Date(created)
    const local = date.toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', second: '2-digit', timeZoneName: 'short' })
    const time = screen.getByText(local)
    expect(time.tagName).toBe('TIME')
    expect(time).toHaveAttribute('datetime', created)
    expect(time).toHaveAttribute('title', `${local} (${created})`)
    expect(time).toHaveAttribute('aria-label', `${local} (${created})`)
  })

  it('does not fabricate metadata or current timestamps for legacy rows and private addresses', async () => {
    request.mockResolvedValue({ data: [{ ID: 'legacy', CreatedAt: '0001-01-01T00:00:00Z', LastSeenAt: 'bad-date' }, { ID: 'local', Browser: '', IPAddress: '127.0.0.1', CountryCode: 'ZZ' }] })
    mount()
    await screen.findByText('127.0.0.1')
    expect(screen.getAllByText('Unknown browser')).toHaveLength(2)
    expect(screen.getAllByText('Unknown')).toHaveLength(7)
    expect(document.querySelector('time')).toBeNull()
    expect(document.querySelector('img[src^="/flags/"]')).toBeNull()
    expect(screen.queryByText('Current session')).not.toBeInTheDocument()
  })

  it('sorts remaining sessions by genuine last activity without changing response order', async () => {
    const data = [other, { ...other, ID: 'newer', Browser: 'Safari', LastSeenAt: '2026-10-09T12:00:00Z' }, current]
    request.mockResolvedValue({ data })
    mount()
    await screen.findByText('Safari')
    const rows = screen.getAllByRole('listitem')
    expect(within(rows[0]).getByText('Firefox')).toBeVisible()
    expect(within(rows[1]).getByText('Safari')).toBeVisible()
    expect(data[0].ID).toBe('other/id')
  })

  it('revokes only the selected opaque ID and refreshes the list', async () => {
    request.mockImplementation(async (method) => method === 'GET' ? { data: [current, other] } : undefined)
    mount()
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke Chrome session at 192.0.2.1' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('DELETE', '/api/private/me/sessions/other%2Fid'))
    await waitFor(() => expect(request.mock.calls.filter(([method]) => method === 'GET')).toHaveLength(2))
    expect(screen.getByRole('button', { name: 'Revoke current session' })).toBeEnabled()
  })

  it('keeps the current session revocable and reports failures without hiding rows', async () => {
    request.mockImplementation(async (method) => {
      if (method === 'DELETE') throw new Error('denied')
      return { data: [current] }
    })
    mount()
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke current session' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not revoke this session')
    expect(request).toHaveBeenCalledWith('DELETE', '/api/private/me/sessions/current')
    expect(screen.getByText('Firefox')).toBeVisible()
  })

  it('shows loading, load failure with retry, and an honest empty state', async () => {
    request.mockRejectedValueOnce(new Error('offline')).mockResolvedValue({ data: [] })
    mount()
    expect(screen.getByRole('status')).toHaveTextContent('Loading sessions')
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load your sessions')
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(await screen.findByText('No active sessions.')).toBeVisible()
  })
})
