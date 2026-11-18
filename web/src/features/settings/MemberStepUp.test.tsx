import '@testing-library/jest-dom/vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiProblem } from '@/lib/apiProblem'

import { MembersPage } from './SettingsPages'
import { privateRequest } from './privateApi'

vi.mock('./privateApi', () => ({ privateRequest: vi.fn() }))
const request = vi.mocked(privateRequest)
const member = { user_id: 2, name: 'Alex', email: 'alex@example.test', role: 'viewer', site_ids: null, capabilities: [], billing_admin: false }

afterEach(() => { cleanup(); vi.clearAllMocks() })

function setup() {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}><MembersPage orgId={1} actorRole="owner" sites={[]} /></QueryClientProvider>)
}

describe('member removal', () => {
  it('uses the danger confirmation naming the member', async () => {
    request.mockImplementation(async (method) => (method === 'GET' ? { data: [member] } : undefined) as never)
    setup()
    fireEvent.click(await screen.findByRole('button', { name: /Remove Alex/ }))
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('Alex')
    expect(request).not.toHaveBeenCalledWith('DELETE', expect.anything(), expect.anything())
    fireEvent.click(within(dialog).getAllByRole('button').find((b) => /remove/i.test(b.textContent ?? '') && !/cancel/i.test(b.textContent ?? ''))!)
    await waitFor(() => expect(request).toHaveBeenCalledWith('DELETE', '/api/private/orgs/1/members/2', expect.anything()))
  })

  it('asks for the password when the server wants step-up, then retries', async () => {
    let denied = true
    request.mockImplementation(async (method) => {
      if (method === 'GET') return { data: [member] } as never
      if (method === 'DELETE' && denied) {
        denied = false
        throw new ApiProblem(403, { code: 'reauth_required', title: 'Reauthentication required' })
      }
      return undefined as never
    })
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 204 })
    vi.stubGlobal('fetch', fetchMock)
    setup()
    fireEvent.click(await screen.findByRole('button', { name: /Remove Alex/ }))
    const confirm = await screen.findByRole('alertdialog')
    fireEvent.click(within(confirm).getAllByRole('button').find((b) => /remove/i.test(b.textContent ?? '') && !/cancel/i.test(b.textContent ?? ''))!)
    const reauth = await screen.findByRole('dialog', { name: 'Confirm it is you' })
    fireEvent.change(within(reauth).getByLabelText('Password'), { target: { value: 'pw' } })
    fireEvent.click(within(reauth).getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(request.mock.calls.filter(([m]) => m === 'DELETE')).toHaveLength(2))
    vi.unstubAllGlobals()
  })
})
