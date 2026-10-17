import '@testing-library/jest-dom/vitest'

import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ForgotPasswordForm } from '@/features/auth/ForgotPasswordForm'
import { LoginForm } from '@/features/auth/LoginForm'
import { ApiProblem } from '@/lib/apiProblem'
import { ResetPasswordForm } from './reset.$token'
import { SignupPageContent } from './signup'

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

function renderWithRouter(children: ReactNode) {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 200 })))
  const router = createRouter({ routeTree: createRootRoute({ component: () => children }), history: createMemoryHistory({ initialEntries: ['/'] }) })
  return render(<RouterProvider router={router} />)
}

const problem = (status: number) => new ApiProblem(status, { type: 'about:blank' })

describe('forgot password', () => {
  it('is linked from the sign-in form', async () => {
    renderWithRouter(<LoginForm login={vi.fn()} next="/app" onSuccess={vi.fn()} />)
    expect(await screen.findByRole('link', { name: 'Forgot password?' })).toHaveAttribute('href', '/forgot')
  })

  it('always gives the same neutral reply and then rate-limits resends', async () => {
    const request = vi.fn().mockResolvedValue({ ok: true })
    renderWithRouter(<ForgotPasswordForm request={request} cooldown={2} />)
    fireEvent.change(await screen.findByLabelText('Email'), { target: { value: ' someone@example.test ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Send reset link' }))
    expect(await screen.findByRole('status')).toHaveTextContent('If an account exists for someone@example.test')
    expect(request).toHaveBeenCalledWith('someone@example.test')
    expect(screen.getByRole('button', { name: 'Send another link' })).toBeDisabled()
    expect(screen.getByText('You can request another link in 2 s.')).toBeVisible()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Send another link' })).toBeEnabled(), { timeout: 4_000 })
  })

  it('treats an unknown address exactly like a known one', async () => {
    const request = vi.fn().mockResolvedValue({ ok: false, problem: problem(404) })
    renderWithRouter(<ForgotPasswordForm request={request} />)
    fireEvent.change(await screen.findByLabelText('Email'), { target: { value: 'nobody@example.test' } })
    fireEvent.click(screen.getByRole('button', { name: 'Send reset link' }))
    expect(await screen.findByRole('status')).toHaveTextContent('If an account exists for nobody@example.test')
  })

  it('reports rate limiting as an error', async () => {
    const request = vi.fn().mockResolvedValue({ ok: false, problem: problem(429) })
    renderWithRouter(<ForgotPasswordForm request={request} />)
    fireEvent.change(await screen.findByLabelText('Email'), { target: { value: 'a@example.test' } })
    fireEvent.click(screen.getByRole('button', { name: 'Send reset link' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Too many attempts')
  })
})

describe('reset password', () => {
  it('shows a pending state and hands over to sign-in on success', async () => {
    let resolve: (value: { ok: boolean }) => void = () => undefined
    const submit = vi.fn(() => new Promise<{ ok: boolean }>((done) => { resolve = done }))
    const onDone = vi.fn()
    renderWithRouter(<ResetPasswordForm submit={submit} onDone={onDone} />)
    fireEvent.change(await screen.findByLabelText('New password'), { target: { value: 'new-password-1' } })
    fireEvent.click(screen.getByRole('button', { name: 'Reset password' }))
    expect(await screen.findByRole('button', { name: 'Saving password…' })).toBeDisabled()
    await act(async () => resolve({ ok: true }))
    expect(onDone).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('offers a new link when the token is spent', async () => {
    const submit = vi.fn().mockResolvedValue({ ok: false, problem: new ApiProblem(400, { type: 'about:blank', title: 'Bad request', detail: 'reset token is invalid' }) })
    renderWithRouter(<ResetPasswordForm submit={submit} onDone={vi.fn()} />)
    fireEvent.change(await screen.findByLabelText('New password'), { target: { value: 'new-password-1' } })
    fireEvent.click(screen.getByRole('button', { name: 'Reset password' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('This link is invalid')
    expect(screen.getByRole('link', { name: 'Request a new link' })).toHaveAttribute('href', '/forgot')
  })

  it('lets people check the password they typed', async () => {
    renderWithRouter(<ResetPasswordForm submit={vi.fn()} onDone={vi.fn()} />)
    const input = await screen.findByLabelText('New password')
    expect(input).toHaveAttribute('type', 'password')
    fireEvent.click(screen.getByRole('button', { name: 'Show password' }))
    expect(input).toHaveAttribute('type', 'text')
    expect(screen.getByRole('button', { name: 'Hide password' })).toHaveAttribute('aria-pressed', 'true')
  })
})

describe('sign-in notices', () => {
  it('shows a neutral status after a reset', async () => {
    renderWithRouter(<LoginForm login={vi.fn()} next="/app" onSuccess={vi.fn()} notice="reset" />)
    expect(await screen.findByRole('status')).toHaveTextContent('Password updated. Sign in with your new password.')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})

describe('sign-up settings failure', () => {
  it('reports the failure with a retry instead of claiming sign-ups are invite-only', async () => {
    const onRetryConfig = vi.fn()
    renderWithRouter(<SignupPageContent configFailed onRetryConfig={onRetryConfig} submit={vi.fn()} onSuccess={vi.fn()} />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load the sign-up settings')
    expect(screen.queryByText('Signups are invite-only.')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(onRetryConfig).toHaveBeenCalled()
  })
})
