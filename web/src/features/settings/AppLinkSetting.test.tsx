import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { AppLinkSetting } from './AppLinkSetting'

const sites = [
  { id: 4, name: 'Website', kind: 'web' },
  { id: 12, name: 'Desktop app', kind: 'app' },
  { id: 13, name: 'Mobile app', kind: 'app' },
]

function renderSetting() {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <AppLinkSetting orgId={3} siteId={4} />
    </QueryClientProvider>,
  )
}

function mockApi(options: { apps?: typeof sites; linked?: number | null } = {}) {
  privateRequest.mockImplementation((method: string, path: string, request?: { body?: { app_site_id: number | null } }) => {
    if (path.startsWith('/api/private/sites?')) return Promise.resolve({ data: options.apps ?? sites })
    if (method === 'PUT') return Promise.resolve({ app_site_id: request?.body?.app_site_id ?? null })
    return Promise.resolve({ app_site_id: options.linked ?? null })
  })
}

describe('AppLinkSetting', () => {
  beforeEach(() => vi.clearAllMocks())
  afterEach(cleanup)

  it('lists only app projects and shows the current link', async () => {
    mockApi({ linked: 13 })
    renderSetting()
    const select = await screen.findByLabelText('Send diagnostic reports to app')
    await waitFor(() => expect(select).toHaveValue('13'))
    expect(screen.getByRole('option', { name: 'Desktop app' })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'Website' })).not.toBeInTheDocument()
  })

  it('links to the chosen app and unlinks with None', async () => {
    mockApi()
    renderSetting()
    const select = await screen.findByLabelText('Send diagnostic reports to app')
    fireEvent.change(select, { target: { value: '12' } })
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('PUT', '/api/private/sites/4/app-link', { orgId: 3, body: { app_site_id: 12 } }))
    await waitFor(() => expect(select).toHaveValue('12'))
    fireEvent.change(select, { target: { value: '' } })
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('PUT', '/api/private/sites/4/app-link', { orgId: 3, body: { app_site_id: null } }))
  })

  it('renders nothing when the organization has no app and the site is unlinked', async () => {
    mockApi({ apps: [sites[0]] })
    renderSetting()
    await waitFor(() => expect(privateRequest).toHaveBeenCalledTimes(2))
    expect(screen.queryByLabelText('Send diagnostic reports to app')).not.toBeInTheDocument()
  })
})
