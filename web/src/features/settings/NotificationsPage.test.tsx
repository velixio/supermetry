import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { del, get, patch, post } = vi.hoisted(() => ({
  del: vi.fn(),
  get: vi.fn(),
  patch: vi.fn(),
  post: vi.fn(),
}))

vi.mock('@/lib/api', () => ({ api: { DELETE: del, GET: get, PATCH: patch, POST: post } }))

import { NotificationsPage } from './NotificationsPage'

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <NotificationsPage orgId={3} />
    </QueryClientProvider>,
  )
}

describe('NotificationsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    get.mockResolvedValue({
      data: {
        data: [{
          id: 8,
          org_id: 3,
          kind: 'email',
          name: 'Operations',
          enabled: true,
          config: { recipients: ['ops@example.com'] },
          created_at: '2026-10-08T00:00:00Z',
        }],
      },
      response: { ok: true, status: 200 },
    })
    post.mockResolvedValue({ data: {}, response: { ok: true, status: 200 } })
    patch.mockResolvedValue({ data: {}, response: { ok: true, status: 200 } })
    del.mockResolvedValue({ data: {}, response: { ok: true, status: 204 } })
  })

  afterEach(cleanup)

  it('creates a webhook channel without exposing its secret afterwards', async () => {
    renderPage()
    expect(await screen.findByText('Operations')).toBeVisible()

    fireEvent.click(screen.getByRole('button', { name: 'Add channel' }))
    fireEvent.change(screen.getByLabelText('Channel type'), { target: { value: 'webhook' } })
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Deploy hook' } })
    fireEvent.change(screen.getByLabelText('Endpoint URL'), { target: { value: 'https://hooks.example.test/panel' } })
    fireEvent.change(screen.getByLabelText('Signing secret'), { target: { value: 'secret-value' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create channel' }))

    await waitFor(() => expect(post).toHaveBeenCalledWith(
      '/api/private/orgs/3/channels',
      expect.objectContaining({
        body: {
          kind: 'webhook',
          name: 'Deploy hook',
          enabled: true,
          config: { url: 'https://hooks.example.test/panel', secret: 'secret-value' },
        },
      }),
    ))
    expect(screen.queryByDisplayValue('secret-value')).not.toBeInTheDocument()
  })

  it('sends a test notification and reports success', async () => {
    renderPage()
    await screen.findByText('Operations')
    fireEvent.click(screen.getByRole('button', { name: 'Send test to Operations' }))
    expect(await screen.findByRole('status')).toHaveTextContent('Test notification sent')
  })

  it('does not offer unsafe editing when a configured endpoint is redacted', async () => {
    get.mockResolvedValue({
      data: {
        data: [{
          id: 9,
          org_id: 3,
          kind: 'webhook',
          name: 'Deploy hook',
          config: { configured: true },
          created_at: '2026-10-08T00:00:00Z',
        }],
      },
      response: { ok: true, status: 200 },
    })
    renderPage()
    await screen.findByText('Deploy hook')
    expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument()
  })

  it('creates a disabled Telegram channel with a write-only bot token', async () => {
    renderPage()
    await screen.findByText('Operations')
    fireEvent.click(screen.getByRole('button', { name: 'Add channel' }))
    fireEvent.change(screen.getByLabelText('Channel type'), { target: { value: 'telegram' } })
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Telegram incidents' } })
    fireEvent.change(screen.getByLabelText('Bot token'), { target: { value: '123456:fake-token-value-for-tests' } })
    fireEvent.change(screen.getByLabelText('Chat ID or channel username'), { target: { value: '-1001234567890' } })
    fireEvent.click(screen.getByLabelText('Enabled'))
    fireEvent.click(screen.getByRole('button', { name: 'Create channel' }))

    await waitFor(() => expect(post).toHaveBeenCalledWith(
      '/api/private/orgs/3/channels',
      expect.objectContaining({ body: {
        kind: 'telegram', name: 'Telegram incidents', enabled: false,
        config: { bot_token: '123456:fake-token-value-for-tests', chat_id: '-1001234567890' },
      } }),
    ))
    expect(screen.queryByDisplayValue('123456:fake-token-value-for-tests')).not.toBeInTheDocument()
  })

  it('edits Telegram configuration without reading or resending the configured token', async () => {
    get.mockResolvedValue({
      data: { data: [{
        id: 10, org_id: 3, kind: 'telegram', name: 'Telegram ops', enabled: false,
        config: { configured: true, chat_id: '-1001234567890' }, created_at: '2026-10-08T00:00:00Z',
      }] },
      response: { ok: true, status: 200 },
    })
    renderPage()
    await screen.findByText('Telegram ops')
    expect(screen.getByText(/Disabled/)).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }))
    expect(screen.getByLabelText('Bot token')).toHaveValue('')
    expect(screen.getByLabelText('Chat ID or channel username')).toHaveValue('-1001234567890')
    fireEvent.change(screen.getByLabelText('Chat ID or channel username'), { target: { value: '@panel_alerts' } })
    fireEvent.click(screen.getByLabelText('Enabled'))
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))

    await waitFor(() => expect(patch).toHaveBeenCalledWith(
      '/api/private/orgs/3/channels/10',
      expect.objectContaining({ body: {
        kind: 'telegram', name: 'Telegram ops', enabled: true,
        config: { bot_token: '', chat_id: '@panel_alerts' },
      } }),
    ))
  })

  it('shows a useful test-delivery failure', async () => {
    post.mockResolvedValueOnce({
      error: { detail: 'Test notification could not be delivered; verify the channel credentials and destination.' },
      response: { ok: false, status: 502 },
    })
    renderPage()
    await screen.findByText('Operations')
    fireEvent.click(screen.getByRole('button', { name: 'Send test to Operations' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('verify the channel credentials')
  })
})
