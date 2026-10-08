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
})
