import '@testing-library/jest-dom/vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
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
    expect(request).not.toHaveBeenCalledWith('PATCH', expect.anything(), expect.anything())
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
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
      fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    } else {
      fireEvent.click(screen.getByRole('checkbox', { name: /Read profiles/ }))
      fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
      // Removing a grant takes access away, so it needs confirmation.
      const dialog = await screen.findByRole('alertdialog', { name: 'Reduce access for Alex?' })
      fireEvent.click(within(dialog).getByRole('button', { name: 'Reduce access' }))
    }
    await waitFor(() => expect(request).toHaveBeenCalledWith(
      'PATCH', '/api/private/orgs/1/members/2',
      { orgId: 1, body: field === 'site' ? { site_ids: null } : { capabilities: [] } },
    ))
  })

  it('hides hosted badges on existing invitations and revokes without rewriting grants', async () => {
    request.mockResolvedValue({ data: [{
      id: 9, email: 'teammate@example.test', role: 'viewer', site_ids: [],
      capabilities: [], billing_admin: true, expires_at: '2026-10-24T12:00:00Z',
    }] })
    provider(<InvitationsPage orgId={1} actorRole="owner" sites={sites} now={Date.parse('2026-10-17T12:00:00Z')} />)
    await screen.findByText(/teammate@example.test/)
    expect(screen.queryByText(/billing/i)).not.toBeInTheDocument()
    expect(screen.getByText(/Expires /)).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Revoke invitation for teammate@example.test' }))
    expect(request).not.toHaveBeenCalledWith('DELETE', expect.anything(), expect.anything())
    const dialog = await screen.findByRole('alertdialog', { name: 'Revoke the invitation for teammate@example.test?' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Revoke invitation' }))
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

  it('marks the current user, removes another member after confirmation and confirms it', async () => {
    request.mockImplementation(async (method) => (method === 'GET' ? { data: [
      { user_id: 1, name: 'Me', email: 'me@example.test', role: 'owner', site_ids: null, capabilities: [], billing_admin: false },
      { user_id: 2, name: 'Alex', email: 'alex@example.test', role: 'viewer', site_ids: null, capabilities: [], billing_admin: false },
    ] } : undefined) as never)
    provider(<MembersPage orgId={1} orgName="Acme" actorRole="owner" sites={sites} currentUserId={1} />)
    expect(await screen.findByText('(you)')).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Remove Me from the organization' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Remove Alex from the organization' }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Remove Alex?' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove member' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('DELETE', '/api/private/orgs/1/members/2', { orgId: 1 }))
    expect(await screen.findByText('Alex was removed.')).toBeVisible()
  })

  it('lets a viewer leave, and explains the last-owner rule', async () => {
    const { ApiProblem } = await import('@/lib/apiProblem')
    request.mockRejectedValueOnce(new ApiProblem(409, { detail: 'the last owner cannot leave' }))
    provider(<MembersPage orgId={1} orgName="Acme" actorRole="viewer" sites={[]} />)
    fireEvent.click(screen.getByRole('button', { name: 'Leave organization' }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Leave Acme?' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Leave' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('You are the last owner.')
    expect(request).toHaveBeenCalledWith('DELETE', '/api/private/orgs/1/membership', { orgId: 1 })
  })

  it('confirms an invitation was created and resends with the same access', async () => {
    request.mockImplementation(async (method) => (method === 'GET'
      ? { data: [{ id: 9, email: 'late@example.test', role: 'editor', site_ids: [7], capabilities: ['replay.read'], billing_admin: false, expires_at: '2026-10-01T00:00:00Z' }] }
      : method === 'POST' ? { link: 'https://supermetry.example/invite?token=abc' } : undefined) as never)
    provider(<InvitationsPage orgId={1} actorRole="owner" sites={sites} now={Date.parse('2026-10-17T12:00:00Z')} />)
    fireEvent.change(screen.getByRole('textbox', { name: 'Email' }), { target: { value: 'new@example.test' } })
    fireEvent.click(screen.getByRole('button', { name: 'Invite' }))
    expect(await screen.findByText(/Invitation created for new@example.test/)).toHaveAttribute('role', 'status')
    expect(screen.getByRole('button', { name: 'Copy invitation link' })).toBeVisible()

    expect(await screen.findByText('Expired')).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Revoke invitation for late@example.test' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Resend invitation to late@example.test' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('POST', '/api/private/orgs/1/invitations', {
      orgId: 1, body: { email: 'late@example.test', role: 'editor', site_ids: [7], capabilities: ['replay.read'] },
    }))
    expect(await screen.findByText(/A new invitation was sent to late@example.test/)).toBeVisible()
  })
})
