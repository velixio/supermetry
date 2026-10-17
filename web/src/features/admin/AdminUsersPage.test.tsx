import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const mutate = vi.fn()
const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', async (original) => ({
  ...await original<typeof import('@/features/settings/privateApi')>(),
  privateRequest,
}))
vi.mock('./adminApi', async (original) => ({
  ...await original<typeof import('./adminApi')>(),
  useAdminUsers: () => ({ isPending: false, error: null, data: { data: [
    { id: 1, email: 'root@example.test', name: 'Root', instance_admin: true, disabled: false, password_reset_required: false, totp_enabled: true, created_at: '2026-10-01T00:00:00Z', last_login_at: '2026-10-12T08:00:00Z', memberships: [] },
    { id: 2, email: 'sam@example.test', name: 'Sam', instance_admin: false, disabled: true, password_reset_required: true, totp_enabled: false, created_at: '2026-10-02T00:00:00Z', memberships: [] },
    { id: 3, email: 'cara@example.test', name: 'Cara', instance_admin: false, disabled: false, password_reset_required: false, totp_enabled: false, created_at: '2026-10-02T00:00:00Z', memberships: [] },
  ] } }),
  useUpdateAdminUser: () => ({ mutate, isPending: false, error: null, reset: vi.fn() }),
}))

import { AdminUsersPage } from './AdminUsersPage'

function renderPage(node: ReactNode) {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false } } })}>{node}</QueryClientProvider>)
}

describe('AdminUsersPage', () => {
  beforeEach(() => {
    mutate.mockClear()
    privateRequest.mockReset()
  })
  afterEach(cleanup)

  it('shows text status badges and a never-signed-in marker', () => {
    renderPage(<AdminUsersPage currentUserId={1} />)
    expect(screen.getByRole('heading', { level: 1, name: 'Users' })).toBeVisible()
    expect(screen.getByText('Instance admin')).toBeVisible()
    expect(screen.getByText('Disabled')).toBeVisible()
    expect(screen.getAllByText('Never').length).toBeGreaterThan(0)
  })

  it('does not let admins disable themselves and enables a disabled user without confirmation', () => {
    renderPage(<AdminUsersPage currentUserId={1} />)
    expect(screen.getByRole('button', { name: 'Disable root@example.test' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Enable sam@example.test' }))
    expect(mutate).toHaveBeenCalledWith({ id: 2, patch: { disabled: false } }, expect.anything())
  })

  it('asks in a dialog, not window.confirm, before removing instance admin rights', async () => {
    const confirm = vi.spyOn(window, 'confirm')
    renderPage(<AdminUsersPage currentUserId={2} />)
    fireEvent.click(screen.getByRole('button', { name: 'Remove instance admin: root@example.test' }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Remove instance admin from root@example.test?' })
    expect(mutate).not.toHaveBeenCalled()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Confirm' }))
    expect(mutate).toHaveBeenCalledWith({ id: 1, patch: { instance_admin: false } }, expect.anything())
    expect(confirm).not.toHaveBeenCalled()
    confirm.mockRestore()
  })

  it('creates a user and shows the one-time link when mail is off', async () => {
    privateRequest.mockResolvedValue({ id: 9, email: 'new@example.test', name: 'new', instance_admin: false, disabled: false, password_reset_required: true, totp_enabled: false, created_at: '2026-10-17T00:00:00Z', memberships: [], reset_url: 'https://supermetry.example/reset/abc' })
    renderPage(<AdminUsersPage currentUserId={1} />)
    fireEvent.click(screen.getByRole('button', { name: 'Add user' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add a user' })
    fireEvent.change(within(dialog).getByLabelText('Email'), { target: { value: 'new@example.test' } })
    fireEvent.change(within(dialog).getByLabelText('Organization name'), { target: { value: 'New Co' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create user' }))
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('POST', '/api/private/admin/users', { body: { email: 'new@example.test', org_name: 'New Co', send_reset: true } }))
    expect(await screen.findByRole('dialog', { name: 'One-time password link' })).toHaveTextContent('https://supermetry.example/reset/abc')
    expect(screen.getByText('User new@example.test created.')).toHaveAttribute('role', 'status')
  })

  it('signs in as a user after re-entering the admin password and code', async () => {
    privateRequest.mockResolvedValue(undefined)
    const onImpersonated = vi.fn()
    renderPage(<AdminUsersPage currentUserId={1} adminTotpEnabled onImpersonated={onImpersonated} />)
    expect(screen.getByRole('button', { name: 'Sign in as root@example.test' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Sign in as sam@example.test' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Sign in as cara@example.test' }))
    const dialog = await screen.findByRole('dialog', { name: 'Sign in as cara@example.test?' })
    expect(dialog).toHaveTextContent('1-hour session')
    fireEvent.change(within(dialog).getByLabelText('Your password'), { target: { value: 'admin-password' } })
    fireEvent.change(within(dialog).getByLabelText('Your authentication code'), { target: { value: '123456' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Sign in as user' }))
    await waitFor(() => expect(onImpersonated).toHaveBeenCalled())
    expect(privateRequest).toHaveBeenCalledWith('POST', '/api/private/admin/users/3/impersonate', { body: { password: 'admin-password', totp_code: '123456' } })
  })
})
