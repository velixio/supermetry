import '@testing-library/jest-dom/vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MembersPage, InvitationsPage } from './SettingsPages'
import { privateRequest } from './privateApi'

vi.mock('./privateApi', () => ({ privateRequest: vi.fn() }))
const request = vi.mocked(privateRequest)
const sites = [{ id: 7, name: 'Docs' }]
function provider(children: React.ReactNode) {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({
          defaultOptions: {
            queries: { retry: false },
            mutations: { retry: false },
          },
        })
      }
    >
      {children}
    </QueryClientProvider>,
  )
}
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('member permission requests', () => {
  it('sends only the role on role changes, preserving no-site and sensitive grants server-side', async () => {
    request.mockResolvedValue({
      data: [
        {
          user_id: 2,
          name: 'Alex',
          email: 'alex@example.test',
          role: 'viewer',
          site_ids: [],
          capabilities: ['profiles.read'],
          billing_admin: true,
        },
      ],
    })
    provider(<MembersPage orgId={1} actorRole="owner" sites={sites} />)
    fireEvent.change(
      await screen.findByRole('combobox', { name: 'Member role' }),
      { target: { value: 'editor' } },
    )
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith(
        'PATCH',
        '/api/private/orgs/1/members/2',
        { orgId: 1, body: { role: 'editor' } },
      ),
    )
  })

  it('creates a billing-only invitation with explicit no sites and no sensitive grants', async () => {
    request.mockImplementation(async (method) =>
      method === 'GET' ? { data: [] } : ({ link: '/invite/example' } as never),
    )
    provider(<InvitationsPage orgId={1} actorRole="owner" sites={sites} />)
    expect(
      screen.queryByRole('option', { name: 'Owner' }),
    ).not.toBeInTheDocument()
    fireEvent.change(screen.getByRole('textbox', { name: 'Email' }), {
      target: { value: 'billing@example.test' },
    })
    fireEvent.change(screen.getByLabelText('Site access'), {
      target: { value: 'none' },
    })
    fireEvent.click(screen.getByRole('checkbox', { name: /Billing Admin/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Invite' }))
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith(
        'POST',
        '/api/private/orgs/1/invitations',
        {
          orgId: 1,
          body: {
            email: 'billing@example.test',
            role: 'viewer',
            site_ids: [],
            capabilities: [],
            billing_admin: true,
          },
        },
      ),
    )
  })

  it('does not read membership management APIs for a billing-only viewer', () => {
    provider(
      <>
        <MembersPage orgId={1} actorRole="viewer" sites={[]} />
        <InvitationsPage orgId={1} actorRole="viewer" sites={[]} />
      </>,
    )
    expect(request).not.toHaveBeenCalled()
  })
})
