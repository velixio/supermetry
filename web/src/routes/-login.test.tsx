import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { loginRedirect } from './app.$org'
import { LoginForm, authResultMessage, createAuthApi } from './login'
import { en } from '@/messages/en'

afterEach(cleanup)
beforeEach(() => {
  window.scrollTo = vi.fn()
})

function renderWithRouter(children: ReactNode) {
  const rootRoute = createRootRoute({ component: () => children })
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  return render(<RouterProvider router={router} />)
}

describe('login form', () => {
  it('uses the dark auth surface by default', () => {
    render(<LoginForm login={vi.fn()} next="/" onSuccess={vi.fn()} />)

    expect(screen.getByRole('main')).toHaveClass('dark', 'bg-background', 'text-foreground')
  })

  it('turns auth network failures into a generic message', async () => {
    await expect(
      authResultMessage(() => Promise.reject(new Error('offline'))),
    ).resolves.toBe(en.auth.genericError)
  })

  it('explains password policy failures', async () => {
    await expect(
      authResultMessage(() => Promise.resolve({ ok: false, type: 'password-policy' })),
    ).resolves.toBe(en.auth.passwordPolicy)
  })

  it('keeps authentication 401 problems in the login flow', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response('{"type":"https://supermetry.com/problems/totp-required"}', {
        status: 401,
        headers: { 'Content-Type': 'application/problem+json' },
      }),
    )
    const auth = createAuthApi(fetchMock)

    const result = await auth('/api/private/auth/login', {
      email: 'o@x.io',
      password: 'pw-123456789',
    })

    expect(result).toEqual({ ok: false, type: 'totp-required' })
    expect(fetchMock).toHaveBeenCalledOnce()
  })

  it('redirects to login with next on expiry and returns after login', async () => {
    const login = vi.fn().mockResolvedValue({ ok: true })
    const onSuccess = vi.fn()
    const redirect = loginRedirect('/app/acme/blog', '?period=30d')
    const next = new URL(redirect, window.location.origin).searchParams.get('next')
    render(
      <LoginForm
        login={login}
        next={next ?? '/'}
        onSuccess={onSuccess}
      />,
    )

    fireEvent.change(screen.getByLabelText(/email/i), {
      target: { value: 'o@x.io' },
    })
    fireEvent.change(screen.getByLabelText(/password/i), {
      target: { value: 'pw-123456789' },
    })
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }))

    await waitFor(() =>
      expect(onSuccess).toHaveBeenCalledWith('/app/acme/blog?period=30d'),
    )
  })

  it('asks for a TOTP code and sends it on the next attempt', async () => {
    const login = vi
      .fn()
      .mockResolvedValueOnce({ ok: false, type: 'totp-required' })
      .mockResolvedValueOnce({ ok: true })
    render(<LoginForm login={login} next="/" onSuccess={vi.fn()} />)

    fireEvent.change(screen.getByLabelText(/email/i), {
      target: { value: 'o@x.io' },
    })
    fireEvent.change(screen.getByLabelText(/password/i), {
      target: { value: 'pw-123456789' },
    })
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }))

    const code = await screen.findByLabelText(/authentication code/i)
    fireEvent.change(code, { target: { value: '123456' } })
    fireEvent.click(screen.getByRole('button', { name: /verify/i }))

    await waitFor(() => expect(login).toHaveBeenCalledTimes(2))
    expect(login).toHaveBeenLastCalledWith(
      expect.objectContaining({ totp: '123456' }),
    )
  })

  it('rejects backslash-based external return URLs', async () => {
    const onSuccess = vi.fn()
    render(
      <LoginForm
        login={vi.fn().mockResolvedValue({ ok: true })}
        next="/\\evil.test"
        onSuccess={onSuccess}
      />,
    )

    fireEvent.change(screen.getByLabelText(/email/i), {
      target: { value: 'o@x.io' },
    })
    fireEvent.change(screen.getByLabelText(/password/i), {
      target: { value: 'pw-123456789' },
    })
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }))

    await waitFor(() => expect(onSuccess).toHaveBeenCalledWith('/'))
  })

  it('offers public signup only when signup is open', async () => {
    renderWithRouter(
      <LoginForm
        login={vi.fn()}
        next="/"
        onSuccess={vi.fn()}
        signup="open"
      />,
    )

    expect(await screen.findByRole('link', { name: 'Create an account' })).toHaveAttribute(
      'href',
      '/signup',
    )
  })

  it('does not offer public signup when signup is invite only', () => {
    render(
      <LoginForm
        login={vi.fn()}
        next="/"
        onSuccess={vi.fn()}
        signup="invite"
      />,
    )

    expect(screen.queryByRole('link', { name: 'Create an account' })).toBeNull()
  })
})
