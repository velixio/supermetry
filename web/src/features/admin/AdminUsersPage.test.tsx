import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const mutate = vi.fn()
vi.mock('./adminApi', async (original) => ({
  ...await original<typeof import('./adminApi')>(),
  useAdminUsers: () => ({ isPending: false, error: null, data: { data: [
    { id: 1, email: 'root@example.test', name: 'Root', instance_admin: true, disabled: false, password_reset_required: false, totp_enabled: true, created_at: '2026-10-01T00:00:00Z', last_login_at: '2026-10-12T08:00:00Z', memberships: [] },
    { id: 2, email: 'sam@example.test', name: 'Sam', instance_admin: false, disabled: true, password_reset_required: true, totp_enabled: false, created_at: '2026-10-02T00:00:00Z', memberships: [] },
  ] } }),
  useUpdateAdminUser: () => ({ mutate, isPending: false, error: null }),
}))

import { AdminUsersPage } from './AdminUsersPage'

describe('AdminUsersPage', () => {
  beforeEach(() => mutate.mockClear())
  afterEach(cleanup)

  it('shows text status badges and a never-signed-in marker', () => {
    render(<AdminUsersPage currentUserId={1} />)
    expect(screen.getByRole('heading', { level: 1, name: 'Users' })).toBeVisible()
    expect(screen.getByText('Instance admin')).toBeVisible()
    expect(screen.getByText('Disabled')).toBeVisible()
    expect(screen.getByText('Never')).toBeVisible()
  })

  it('does not let admins disable themselves and enables a disabled user without confirmation', () => {
    render(<AdminUsersPage currentUserId={1} />)
    expect(screen.getByRole('button', { name: 'Disable root@example.test' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Enable sam@example.test' }))
    expect(mutate).toHaveBeenCalledWith({ id: 2, patch: { disabled: false } })
  })

  it('asks before removing instance admin rights', () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    render(<AdminUsersPage currentUserId={2} />)
    fireEvent.click(screen.getByRole('button', { name: 'Remove instance admin: root@example.test' }))
    expect(confirm).toHaveBeenCalled()
    expect(mutate).not.toHaveBeenCalled()
    confirm.mockReturnValue(true)
    fireEvent.click(screen.getByRole('button', { name: 'Remove instance admin: root@example.test' }))
    expect(mutate).toHaveBeenCalledWith({ id: 1, patch: { instance_admin: false } })
    confirm.mockRestore()
  })
})
