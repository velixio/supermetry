import '@testing-library/jest-dom/vitest'
import {
  QueryClient,
  QueryClientProvider,
  useQuery,
} from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import {
  AuthorizationCacheBoundary,
  authorizationScope,
} from './authorization-cache'
import { profileKeys } from '@/features/profiles/api'

afterEach(cleanup)

describe('authorization cache lifecycle', () => {
  it('drops sensitive data before mounting content under a changed scope and on sign-out', () => {
    const client = new QueryClient()
    const keys = [
      profileKeys.detail(1, 7, 123),
      ['replay', 7, 'data', '123'],
      ['sessions', 7, 'list'],
      ['sites', 7, 'events'],
      ['retention', { site_id: 7, identity: 'profile' }],
    ]
    for (const key of keys)
      client.setQueryData(key, { private: 'prior access' })
    client.setQueryData(['stats', 7], { total: 10 })
    function Probe() {
      return (
        <p>
          {keys.some((key) => client.getQueryData(key))
            ? 'Cached sensitive data'
            : 'No sensitive cache'}
        </p>
      )
    }
    const view = render(
      <QueryClientProvider client={client}>
        <AuthorizationCacheBoundary scope="original">
          <Probe />
        </AuthorizationCacheBoundary>
      </QueryClientProvider>,
    )
    expect(screen.getByText('Cached sensitive data')).toBeVisible()
    view.rerender(
      <QueryClientProvider client={client}>
        <AuthorizationCacheBoundary scope="revoked">
          <Probe />
        </AuthorizationCacheBoundary>
      </QueryClientProvider>,
    )
    expect(screen.getByText('No sensitive cache')).toBeVisible()
    expect(screen.queryByText('Cached sensitive data')).not.toBeInTheDocument()
    expect(client.getQueryData(['stats', 7])).toEqual({ total: 10 })
    client.setQueryData(keys[0], { private: 'newly granted data' })
    view.unmount()
    expect(client.getQueryData(keys[0])).toBeUndefined()
  })

  it('cancels in-flight reads and does not restore their cache on a late response', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    let finish!: (data: string) => void
    let signal: AbortSignal | undefined
    function Pending() {
      useQuery({
        queryKey: ['replay', 7, 'data', '123'],
        queryFn: (context) => {
          signal = context.signal
          return new Promise<string>((resolve) => {
            finish = resolve
          })
        },
      })
      return <p>Loading replay</p>
    }
    const view = render(
      <QueryClientProvider client={client}>
        <AuthorizationCacheBoundary scope="allowed">
          <Pending />
        </AuthorizationCacheBoundary>
      </QueryClientProvider>,
    )
    await waitFor(() => expect(signal).toBeDefined())
    view.rerender(
      <QueryClientProvider client={client}>
        <AuthorizationCacheBoundary scope="revoked">
          <p>Permission revoked</p>
        </AuthorizationCacheBoundary>
      </QueryClientProvider>,
    )
    expect(signal?.aborted).toBe(true)
    finish('private replay')
    await Promise.resolve()
    expect(client.getQueryData(['replay', 7, 'data', '123'])).toBeUndefined()
  })

  it('treats account, organization, role, grants, and site scope changes as new authorization contexts', () => {
    const membership = {
      org: { id: 1, name: 'Acme', slug: 'acme' },
      role: 'viewer' as const,
      sites: [{ id: 7, name: 'Docs' }],
      site_ids: [7],
      effective_capabilities: ['profiles.read'],
    }
    const original = authorizationScope(1, membership)
    for (const next of [
      authorizationScope(2, membership),
      authorizationScope(1, { ...membership, role: 'editor' }),
      authorizationScope(1, { ...membership, effective_capabilities: [] }),
      authorizationScope(1, { ...membership, site_ids: [], sites: [] }),
      authorizationScope(1, {
        ...membership,
        org: { ...membership.org, id: 2 },
      }),
    ])
      expect(next).not.toBe(original)
  })
})
