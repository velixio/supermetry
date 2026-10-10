import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))

vi.mock('@/lib/api', () => ({ api: { GET: get, POST: post } }))
vi.mock('@/lib/session-lifecycle', () => ({ registerPrivateTask: () => () => undefined }))

import { DataPurgeCard } from './DataPurgeCard'

function ok(data: unknown, status = 200) {
  return { data, response: new Response(null, { status }) }
}

function renderCard() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <DataPurgeCard orgId={3} siteId={7} />
    </QueryClientProvider>,
  )
}

function fill(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } })
}

describe('DataPurgeCard', () => {
  beforeEach(() => {
    get.mockResolvedValue(ok({ data: [] }))
  })
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('sends the filters and password, then lists the request', async () => {
    post.mockResolvedValue(ok({ id: 1 }, 202))
    renderCard()
    fill('From', '2026-10-01')
    fill('To', '2026-10-05')
    fill('Source', ' spam.test ')
    fill('Your password', 'pw-123456789')
    fireEvent.click(screen.getByRole('button', { name: 'Delete matching traffic' }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post.mock.calls[0][0]).toBe('/api/private/sites/7/purge-requests')
    expect(post.mock.calls[0][1].body).toEqual({
      from: '2026-10-01', to: '2026-10-05', source: 'spam.test', country: '', password: 'pw-123456789', totp_code: undefined,
    })
  })

  it('shows the server message when the request is refused', async () => {
    post.mockResolvedValue({ error: { detail: 'password or verification code is invalid' }, response: new Response(null, { status: 401 }) })
    renderCard()
    fill('From', '2026-10-01')
    fill('To', '2026-10-05')
    fill('Country', 'US')
    fill('Your password', 'wrong')
    fireEvent.click(screen.getByRole('button', { name: 'Delete matching traffic' }))
    expect(await screen.findByText('password or verification code is invalid')).toBeInTheDocument()
  })

  it('disables the button while a deletion is running', async () => {
    get.mockResolvedValue(ok({ data: [{ id: 1, from: '2026-10-01', to: '2026-10-05', source: 'spam.test', country: '', status: 'running', sessions_deleted: 0, created_at: '2026-10-10T10:00:00Z' }] }))
    renderCard()
    expect(await screen.findByRole('button', { name: 'A deletion is in progress' })).toBeDisabled()
    expect(screen.getByText(/spam\.test: in progress/)).toBeInTheDocument()
  })
})
