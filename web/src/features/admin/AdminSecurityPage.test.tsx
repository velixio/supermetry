import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const savePolicy = vi.fn()
const policyData = { scope: 'off', grace_days: 7 }
const grant = vi.fn()
vi.mock('./adminApi', async (original) => ({
  ...await original<typeof import('./adminApi')>(),
  useTwoFactorPolicy: () => ({ data: policyData, isPending: false, error: null }),
  useUpdateTwoFactorPolicy: () => ({ mutate: savePolicy, isPending: false, isSuccess: false, error: null }),
  useAdminUsers: () => ({ data: { data: [{ id: 5, email: 'ops@example.test', name: 'Ops' }] } }),
  useSetExemption: () => ({ mutate: grant, isPending: false, isSuccess: false, error: null }),
  useRemoveExemption: () => ({ mutate: vi.fn(), isPending: false, isSuccess: false, error: null }),
  useAdminAudit: () => ({ data: { data: [{ id: 1, action: 'admin.policy.update', target_type: 'instance', target_id: 1, metadata: {}, created_at: '2026-10-12T08:00:00Z' }] }, error: null }),
}))

import { AdminSecurityPage } from './AdminSecurityPage'

describe('AdminSecurityPage', () => {
  beforeEach(() => { savePolicy.mockClear(); grant.mockClear() })
  afterEach(cleanup)

  it('saves the chosen scope and grace period', () => {
    render(<AdminSecurityPage />)
    fireEvent.click(screen.getByRole('radio', { name: 'Everyone' }))
    fireEvent.change(screen.getByLabelText('Grace period in days'), { target: { value: '14' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save policy' }))
    expect(savePolicy).toHaveBeenCalledWith({ scope: 'everyone', grace_days: 14 })
  })

  it('requires a user and a reason before granting an exemption', () => {
    render(<AdminSecurityPage />)
    expect(screen.getByRole('button', { name: 'Grant exemption' })).toBeDisabled()
    fireEvent.change(screen.getByLabelText('User'), { target: { value: '5' } })
    fireEvent.change(screen.getByLabelText('Reason'), { target: { value: 'Hardware key lost' } })
    fireEvent.click(screen.getByRole('button', { name: 'Grant exemption' }))
    expect(grant).toHaveBeenCalledWith({ userId: 5, exemption: { reason: 'Hardware key lost', expires_at: undefined } })
    expect(screen.getByText('admin.policy.update')).toBeVisible()
  })
})
