import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'

import { SignupPageContent } from './signup'
import { routeTree } from '../routeTree.gen'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})
beforeEach(() => {
  window.scrollTo = vi.fn()
})

function renderWithRouter(children: ReactNode) {
  const rootRoute = createRootRoute({
    component: () => children,
  })
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  return render(<RouterProvider router={router} />)
}

describe('signup page', () => {
  it('shows a login link when public signup is invite only', async () => {
    renderWithRouter(
      <SignupPageContent
        signup="invite"
        submit={vi.fn()}
        onSuccess={vi.fn()}
      />,
    )

    expect(await screen.findByText(/signup is available by invitation only/i)).toBeVisible()
    expect(screen.getByRole('link', { name: /sign in/i })).toHaveAttribute('href', '/login')
  })

  it('submits the public signup form and redirects to onboarding', async () => {
    const submit = vi.fn().mockResolvedValue({ ok: true })
    const onSuccess = vi.fn()
    renderWithRouter(
      <SignupPageContent
        signup="open"
        submit={submit}
        onSuccess={onSuccess}
      />,
    )

    fireEvent.change(await screen.findByLabelText('Name'), { target: { value: 'Owner' } })
    fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'owner@example.test' } })
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'pw-123456789' } })
    fireEvent.change(screen.getByLabelText('Organization name'), { target: { value: 'Acme' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create account' }))

    await waitFor(() => expect(onSuccess).toHaveBeenCalledWith('/'))
    expect(submit).toHaveBeenCalledWith({
      email: 'owner@example.test',
      name: 'Owner',
      password: 'pw-123456789',
      org_name: 'Acme',
    })
  })

  it('navigates the signup route to onboarding after success', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = input instanceof Request ? input : new Request(input, init)
      const path = new URL(request.url).pathname
      if (path === '/api/private/auth/config') {
        return new Response('{"signup":"open","setup_required":true}', {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      if (path === '/api/private/auth/signup') {
        return new Response('{"authenticated":true}', {
          status: 201,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      if (path === '/api/private/me') {
        return new Response(
          '{"user":{"id":1,"name":"Owner","email":"owner@example.test"},"memberships":[{"org":{"id":1,"name":"Acme","slug":"acme"},"role":"owner","sites":[]}]}',
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        )
      }
      throw new Error(`Unexpected request: ${request.method} ${path}`)
    })
    vi.stubGlobal('fetch', fetchMock)
    const router = createRouter({
      routeTree,
      history: createMemoryHistory({ initialEntries: ['/signup'] }),
    })
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )

    fireEvent.change(await screen.findByLabelText('Name'), { target: { value: 'Owner' } })
    fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'owner@example.test' } })
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'pw-123456789' } })
    fireEvent.change(screen.getByLabelText('Organization name'), { target: { value: 'Acme' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create account' }))

    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
  })
})
