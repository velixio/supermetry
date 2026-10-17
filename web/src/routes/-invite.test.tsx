import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { routeTree } from '../routeTree.gen'
import { InvitationPage, type InvitationPreview } from './invite.$token'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const preview: InvitationPreview = {
  org_name: 'Acme', org_slug: 'acme', email: 'new@example.test', role: 'editor',
  invited_by: 'Olive', expires_at: '2026-10-24T12:00:00Z', account_exists: false,
}

function renderWithRouter(children: ReactNode) {
  const router = createRouter({ routeTree: createRootRoute({ component: () => children }), history: createMemoryHistory({ initialEntries: ['/'] }) })
  return render(<RouterProvider router={router} />)
}

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const requestURL = (input: RequestInfo | URL) => input instanceof Request ? input.url : String(input)

it('submits the canonical invitation route token to the acceptance endpoint', async () => {
  const token = 'issued_token-123'
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const path = new URL(requestURL(input), window.location.origin).pathname
    if (path === `/api/private/invitations/${token}`) return json(preview)
    if (path === '/api/private/me') return json({}, 401)
    if (path === '/api/private/auth/login') return json({}, 401)
    return json({})
  })
  vi.stubGlobal('fetch', fetchMock)
  window.scrollTo = vi.fn()
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [`/invite/${token}`] }),
  })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)

  expect(await screen.findByRole('heading', { name: 'Join Acme' })).toBeVisible()
  expect(screen.getByText('Olive invited you to join Acme on Supermetry.')).toBeVisible()
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Invited User' } })
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'password-1234' } })
  fireEvent.click(screen.getByRole('button', { name: 'Accept invitation' }))

  await waitFor(() => expect(fetchMock.mock.calls.some(([input]) => requestURL(input).includes(`/invitations/${token}/accept`))).toBe(true))
  const [input, init] = fetchMock.mock.calls.find(([candidate]) => requestURL(candidate).includes(`/invitations/${token}/accept`)) as unknown as [RequestInfo | URL, RequestInit | undefined]
  const request = input instanceof Request ? input : new Request(new URL(String(input), window.location.origin), init)
  expect(new URL(request.url).pathname).toBe(`/api/private/invitations/${token}/accept`)
  expect(request.method).toBe('POST')
  expect(await request.json()).toEqual({ name: 'Invited User', password: 'password-1234' })
  // Signing in failed here, so the page hands over to the sign-in page with a neutral notice.
  expect(await screen.findByText('Invitation accepted. Sign in to open your new organization.')).toBeVisible()
})

describe('invitation page', () => {
  const base = {
    token: 't', previewFailed: false, loading: false,
    accept: vi.fn(), signIn: vi.fn(), onJoined: vi.fn(), onNeedsSignIn: vi.fn(), onSignOut: vi.fn(),
  }

  it('lets a signed-in invitee join with one button and no new password', async () => {
    const accept = vi.fn().mockResolvedValue({ ok: true })
    const onJoined = vi.fn()
    render(<InvitationPage {...base} preview={preview} user={{ email: 'New@example.test' }} accept={accept} onJoined={onJoined} />)
    expect(screen.queryByLabelText('Password')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Join as New@example.test' }))
    await waitFor(() => expect(onJoined).toHaveBeenCalledWith('acme'))
    expect(accept).toHaveBeenCalledWith({})
  })

  it('signs a new account in after accepting', async () => {
    const accept = vi.fn().mockResolvedValue({ ok: true })
    const signIn = vi.fn().mockResolvedValue({ ok: true })
    const onJoined = vi.fn()
    render(<InvitationPage {...base} preview={preview} user={null} accept={accept} signIn={signIn} onJoined={onJoined} />)
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'New' } })
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'password-1234' } })
    fireEvent.click(screen.getByRole('button', { name: 'Accept invitation' }))
    await waitFor(() => expect(onJoined).toHaveBeenCalledWith('acme'))
    expect(signIn).toHaveBeenCalledWith('new@example.test', 'password-1234')
  })

  it('shows the failure next to the password and keeps the form usable', async () => {
    const accept = vi.fn().mockResolvedValue({ ok: false, type: 'https://supermetry.dev/problems/password-policy' })
    render(<InvitationPage {...base} preview={preview} user={null} accept={accept} />)
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'New' } })
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'password' } })
    fireEvent.click(screen.getByRole('button', { name: 'Accept invitation' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Use at least 6 characters')
    expect(screen.getByLabelText('Password')).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByRole('button', { name: 'Accept invitation' })).toBeEnabled()
  })

  it('asks the wrong signed-in account to sign out first', () => {
    const onSignOut = vi.fn()
    render(<InvitationPage {...base} preview={preview} user={{ email: 'other@example.test' }} onSignOut={onSignOut} />)
    expect(screen.getByRole('alert')).toHaveTextContent('signed in as other@example.test')
    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }))
    expect(onSignOut).toHaveBeenCalled()
  })

  it('explains an invalid or used link', async () => {
    renderWithRouter(<InvitationPage {...base} preview={null} user={null} />)
    expect(await screen.findByRole('alert')).toHaveTextContent('invalid, has expired, or was already used')
  })

  it('sends someone with an existing account to sign in first', async () => {
    renderWithRouter(<InvitationPage {...base} token="abc" preview={{ ...preview, account_exists: true }} user={null} />)
    expect(await screen.findByRole('link', { name: 'Sign in to accept' })).toHaveAttribute('href', '/login?next=%2Finvite%2Fabc')
    expect(screen.queryByLabelText('Password')).not.toBeInTheDocument()
  })
})
