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

  it.each(['site', 'capabilities'] as const)('omits stored billing grants when editing %s access', async (field) => {
    request.mockResolvedValue({ data: [{
      user_id: 2, name: 'Alex', email: 'alex@example.test', role: 'viewer',
      site_ids: [], capabilities: ['profiles.read'], billing_admin: true,
    }] })
    provider(<MembersPage orgId={1} actorRole="owner" sites={sites} />)
    await screen.findByRole('combobox', { name: 'Member role' })
    expect(screen.queryByText(/billing/i)).not.toBeInTheDocument()
    if (field === 'site') {
      fireEvent.change(screen.getByLabelText('Site access'), { target: { value: 'all' } })
    } else {
      fireEvent.click(screen.getByRole('checkbox', { name: /Read profiles/ }))
    }
    await waitFor(() => expect(request).toHaveBeenCalledWith(
      'PATCH', '/api/private/orgs/1/members/2',
      { orgId: 1, body: field === 'site' ? { site_ids: null } : { capabilities: [] } },
    ))
  })

  it('hides hosted badges on existing invitations and revokes without rewriting grants', async () => {
    request.mockResolvedValue({ data: [{
      id: 9, email: 'teammate@example.test', role: 'viewer', site_ids: [],
      capabilities: [], billing_admin: true,
    }] })
    provider(<InvitationsPage orgId={1} actorRole="owner" sites={sites} />)
    await screen.findByText(/teammate@example.test/)
    expect(screen.queryByText(/billing/i)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Revoke' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith(
      'DELETE', '/api/private/orgs/1/invitations/9', { orgId: 1 },
    ))
  })

  it.each(['owner', 'admin'] as const)('creates an invitation without billing fields as %s', async (actorRole) => {
    request.mockImplementation(async (method) =>
      method === 'GET' ? { data: [] } : ({ link: '/invite/example' } as never),
    )
    provider(<InvitationsPage orgId={1} actorRole={actorRole} sites={sites} />)
    expect(
      screen.queryByRole('option', { name: 'Owner' }),
    ).not.toBeInTheDocument()
    fireEvent.change(screen.getByRole('textbox', { name: 'Email' }), {
      target: { value: 'teammate@example.test' },
    })
    fireEvent.change(screen.getByLabelText('Site access'), {
      target: { value: 'none' },
    })
    expect(screen.queryByText(/billing/i)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Invite' }))
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith(
        'POST',
        '/api/private/orgs/1/invitations',
        {
          orgId: 1,
          body: {
            email: 'teammate@example.test',
            role: 'viewer',
            site_ids: [],
            capabilities: [],
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
