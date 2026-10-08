import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AccountProfilePage } from './AccountProfilePage'

afterEach(cleanup)

describe('account profile self-service', () => {
  it('updates identity and confirms account deletion', async () => {
    const request = vi.fn().mockResolvedValue({ name: 'Updated', email: 'owner@example.test' })
    const onDeleted = vi.fn()
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <AccountProfilePage name="Owner" email="owner@example.test" totpEnabled request={request} onDeleted={onDeleted} />
      </QueryClientProvider>,
    )

    fireEvent.change(screen.getByLabelText('Display name'), { target: { value: 'Updated' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save name' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('PATCH', '/api/private/me', { name: 'Updated' }))

    fireEvent.change(screen.getByLabelText('New email'), { target: { value: 'new@example.test' } })
    fireEvent.change(screen.getByLabelText('Email change password'), { target: { value: 'pw-123456789' } })
    fireEvent.click(screen.getByRole('button', { name: 'Send confirmation' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('POST', '/api/private/me/email', { new_email: 'new@example.test', password: 'pw-123456789' }))

    fireEvent.change(screen.getByLabelText('Current password'), { target: { value: 'pw-123456789' } })
    fireEvent.change(screen.getByLabelText('New password'), { target: { value: 'new-password-1' } })
    fireEvent.click(screen.getByRole('button', { name: 'Change password' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('POST', '/api/private/me/password', { current_password: 'pw-123456789', new_password: 'new-password-1' }))

    fireEvent.change(screen.getByLabelText('Delete account password'), { target: { value: 'new-password-1' } })
    fireEvent.change(screen.getByLabelText('Authentication code'), { target: { value: '123456' } })
    fireEvent.click(screen.getByRole('button', { name: 'Delete account' }))
    expect(await screen.findByText('Delete your account?')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Permanently delete account' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('DELETE', '/api/private/me', { password: 'new-password-1', totp_code: '123456' }))
    expect(onDeleted).toHaveBeenCalledOnce()
  })
})
