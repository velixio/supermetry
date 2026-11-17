import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const save = vi.fn()
const sendTest = vi.fn()
let settings: Record<string, unknown>
let testResult: Record<string, unknown> | undefined
vi.mock('./adminApi', async (original) => ({
  ...await original<typeof import('./adminApi')>(),
  useAdminEmail: () => ({ data: settings, isPending: false, error: null }),
  useSaveAdminEmail: () => ({ mutate: save, isPending: false, isSuccess: false, error: null }),
  useRemoveAdminEmail: () => ({ mutate: vi.fn(), isPending: false, error: null }),
  useTestAdminEmail: () => ({ mutate: sendTest, isPending: false, error: null, data: testResult }),
}))

import { AdminEmailPage } from './AdminEmailPage'

describe('AdminEmailPage', () => {
  beforeEach(() => {
    save.mockClear()
    sendTest.mockClear()
    testResult = undefined
    settings = { source: 'database', host: 'smtp.example.test', port: 587, tls: 'starttls', user: 'mailer', from: 'a@example.com', helo: '', password_set: true }
  })
  afterEach(cleanup)

  it('saves edited settings and never shows the stored password', () => {
    render(<AdminEmailPage />)
    expect(screen.getByLabelText(/^Password/)).toHaveValue('')
    expect(screen.getByText('A password is saved. Leave blank to keep it.')).toBeVisible()
    fireEvent.change(screen.getByLabelText('SMTP host'), { target: { value: 'smtp.other.test' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save email settings' }))
    expect(save).toHaveBeenCalledWith({ host: 'smtp.other.test', port: 587, tls: 'starttls', user: 'mailer', password: '', from: 'a@example.com', helo: '' })
  })

  it('clears the saved password explicitly', () => {
    render(<AdminEmailPage />)
    fireEvent.click(screen.getByRole('button', { name: 'Remove saved password' }))
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ clear_password: true, password: '' }))
  })

  it('shows environment settings read-only', () => {
    settings = { ...settings, source: 'environment' }
    render(<AdminEmailPage />)
    expect(screen.getByRole('note')).toHaveTextContent('SUPERMETRY_SMTP_')
    expect(screen.queryByRole('button', { name: 'Save email settings' })).toBeNull()
    expect(screen.queryByLabelText(/^Password/)).toBeNull()
    expect(screen.getByText('smtp.example.test')).toBeVisible()
  })

  it('sends a test email and shows success or the failed step inline', () => {
    const view = render(<AdminEmailPage />)
    fireEvent.change(screen.getByLabelText('Send to'), { target: { value: 'me@example.test' } })
    fireEvent.click(screen.getByRole('button', { name: 'Send test email' }))
    expect(sendTest).toHaveBeenCalledWith('me@example.test')
    testResult = { ok: true, to: 'me@example.test', note: 'Acceptance is not inbox delivery.' }
    view.rerender(<AdminEmailPage />)
    expect(screen.getByRole('status')).toHaveTextContent('Acceptance is not inbox delivery.')
    testResult = { ok: false, to: 'me@example.test', error: 'authenticate SMTP: 535 bad credentials', note: 'x' }
    view.rerender(<AdminEmailPage />)
    expect(screen.getByRole('alert')).toHaveTextContent('authenticate SMTP: 535 bad credentials')
  })
})
