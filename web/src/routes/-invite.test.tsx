import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, expect, it, vi } from 'vitest'

import { routeTree } from '../routeTree.gen'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

it('submits the canonical invitation route token to the acceptance endpoint', async () => {
  const token = 'issued_token-123'
  const fetchMock = vi.fn(async (_request: Request) => new Response('{}', {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  }))
  vi.stubGlobal('fetch', fetchMock)
  window.scrollTo = vi.fn()
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [`/invite/${token}`] }),
  })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)

  fireEvent.change(await screen.findByLabelText('Name'), { target: { value: 'Invited User' } })
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'password-1234' } })
  fireEvent.click(screen.getByRole('button', { name: 'Accept invitation' }))

  const requestURL = (input: RequestInfo | URL) => input instanceof Request ? input.url : String(input)
  await waitFor(() => expect(fetchMock.mock.calls.some(([input]) => requestURL(input).includes(`/invitations/${token}/accept`))).toBe(true))
  const [input, init] = fetchMock.mock.calls.find(([candidate]) => requestURL(candidate).includes(`/invitations/${token}/accept`)) as unknown as [RequestInfo | URL, RequestInit | undefined]
  const request = input instanceof Request ? input : new Request(new URL(String(input), window.location.origin), init)
  expect(new URL(request.url).pathname).toBe(`/api/private/invitations/${token}/accept`)
  expect(request.method).toBe('POST')
  expect(await request.json()).toEqual({ name: 'Invited User', password: 'password-1234' })
})
