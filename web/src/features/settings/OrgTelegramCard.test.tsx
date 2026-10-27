import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { del, get, post, put } = vi.hoisted(() => ({ del: vi.fn(), get: vi.fn(), post: vi.fn(), put: vi.fn() }))

vi.mock('@/lib/api', () => ({ api: { DELETE: del, GET: get, POST: post, PUT: put } }))

import { OrgTelegramCard } from './OrgTelegramCard'

const eventKinds = [
  { kind: 'star_milestone', label: 'Star milestones', description: 'Round star counts.' },
  { kind: 'star_spike', label: 'Star spikes', description: 'Fast growth.' },
]

const channel = {
  id: 1, kind: 'telegram', enabled: true, chat_id: '-100123', token_set: true,
  events: { star_milestone: true, star_spike: false },
  created_at: '2026-10-27T00:00:00Z', updated_at: '2026-10-27T00:00:00Z',
  last_ok_at: null, last_error: 'Telegram rejected the message (status 400): chat not found', consecutive_failures: 3,
}

function listResponse(channels: unknown[]) {
  return { data: { data: channels, event_kinds: eventKinds }, response: { ok: true, status: 200 } }
}

function renderCard() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><OrgTelegramCard orgId={3} /></QueryClientProvider>)
}

describe('OrgTelegramCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    put.mockResolvedValue({ data: channel, response: { ok: true, status: 200 } })
    post.mockResolvedValue({ data: { ok: true }, response: { ok: true, status: 200 } })
    del.mockResolvedValue({ data: undefined, response: { ok: true, status: 204 } })
  })
  afterEach(cleanup)

  it('shows the saved state without ever showing the token, plus status', async () => {
    get.mockResolvedValue(listResponse([channel]))
    renderCard()
    expect(await screen.findByDisplayValue('-100123')).toBeInTheDocument()
    const token = screen.getByLabelText('Telegram bot token') as HTMLInputElement
    expect(token.type).toBe('password')
    expect(token.value).toBe('')
    expect(token).toHaveAttribute('placeholder', 'Saved')
    expect(screen.getByText(/3 failed in a row/)).toBeInTheDocument()
    expect(screen.getByText(/chat not found/)).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: 'Star milestones' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Star spikes' })).not.toBeChecked()
  })

  it('saves without a token to keep the stored one and sends the ticked events', async () => {
    get.mockResolvedValue(listResponse([channel]))
    renderCard()
    await screen.findByDisplayValue('-100123')
    fireEvent.click(screen.getByRole('checkbox', { name: 'Star spikes' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(put).toHaveBeenCalled())
    const [path, options] = put.mock.calls[0]
    expect(path).toBe('/api/private/orgs/3/notifications/telegram')
    expect(options.body).toEqual({ chat_id: '-100123', enabled: true, events: { star_milestone: true, star_spike: true } })
    expect(options.body).not.toHaveProperty('token')
  })

  it('requires a token for a new channel and sends it once entered', async () => {
    get.mockResolvedValue(listResponse([]))
    renderCard()
    const token = await screen.findByLabelText('Telegram bot token')
    expect(token).toBeRequired()
    expect(screen.queryByRole('button', { name: 'Send test message' })).not.toBeInTheDocument()
    fireEvent.change(token, { target: { value: '123456789:AAEhBOweik6ad9r_QXMENQjcrTu-oSYZ0abc' } })
    fireEvent.change(screen.getByLabelText('Telegram chat ID'), { target: { value: '@my_channel' } })
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Star milestones' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(put).toHaveBeenCalled())
    expect(put.mock.calls[0][1].body).toEqual({
      token: '123456789:AAEhBOweik6ad9r_QXMENQjcrTu-oSYZ0abc', chat_id: '@my_channel', enabled: true, events: { star_milestone: true },
    })
  })

  it('reports test results, including a scrubbed failure', async () => {
    get.mockResolvedValue(listResponse([channel]))
    renderCard()
    const button = await screen.findByRole('button', { name: 'Send test message' })
    fireEvent.click(button)
    expect(await screen.findByText('Test message sent.')).toBeInTheDocument()
    post.mockResolvedValue({ data: { ok: false, error: 'chat not found' }, response: { ok: true, status: 200 } })
    fireEvent.click(button)
    expect(await screen.findByText('Test failed: chat not found')).toBeInTheDocument()
  })

  it('removes the channel', async () => {
    get.mockResolvedValue(listResponse([channel]))
    renderCard()
    fireEvent.click(await screen.findByRole('button', { name: 'Remove' }))
    await waitFor(() => expect(del).toHaveBeenCalled())
    expect(del.mock.calls[0][0]).toBe('/api/private/orgs/3/notifications/telegram')
  })
})
