import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { del, get, post } = vi.hoisted(() => ({
  del: vi.fn(),
  get: vi.fn(),
  post: vi.fn(),
}))

vi.mock('@/lib/api', () => ({ api: { DELETE: del, GET: get, POST: post } }))

import { ApiKeysPage } from './SettingsPages'

function renderPage(role: 'owner' | 'admin' | 'editor' | 'viewer' = 'owner') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <ApiKeysPage orgId={3} role={role} />
    </QueryClientProvider>,
  )
}

describe('ApiKeysPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    get.mockResolvedValue({ data: { data: [] }, response: { ok: true, status: 200 } })
    post.mockResolvedValue({
      data: { id: 7, key: 'pnl_example_secret' },
      response: { ok: true, status: 201 },
    })
    del.mockResolvedValue({ data: undefined, response: { ok: true, status: 204 } })
  })

  afterEach(cleanup)

  it('warns about keys:manage and submits the selected scopes', async () => {
    renderPage()
    await screen.findByRole('button', { name: 'Create key' })

    const manage = screen.getByRole('checkbox', { name: 'Manage API keys' })
    expect(screen.queryByText(/can create and revoke API keys/i)).not.toBeInTheDocument()
    fireEvent.click(manage)
    expect(screen.getByText(/can create and revoke API keys/i)).toBeVisible()

    fireEvent.change(screen.getByLabelText('Key name'), { target: { value: 'Automation' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create key' }))

    await waitFor(() => expect(post).toHaveBeenCalledWith(
      '/api/private/orgs/3/api-keys',
      expect.objectContaining({
        body: { name: 'Automation', scopes: ['stats:read', 'keys:manage'] },
      }),
    ))
  })

  it('does not render key controls for editors', () => {
    renderPage('editor')

    expect(screen.getByRole('alert')).toHaveTextContent('Only owners and admins can manage API keys')
    expect(screen.queryByRole('button', { name: 'Create key' })).not.toBeInTheDocument()
    expect(get).not.toHaveBeenCalled()
  })
})
