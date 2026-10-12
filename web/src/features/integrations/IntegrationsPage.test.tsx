import { renderAuthorized as render } from '@/test/authorization'
import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { privateRequest } from '@/features/settings/privateApi'

import { connectionState, IntegrationsPage, type IntegrationResult } from './IntegrationsPage'
import { filterRows, rangeDates, sortRows } from './SearchConsoleReport'

vi.mock('@/features/settings/privateApi', () => ({ privateRequest: vi.fn() }))

const request = vi.mocked(privateRequest)

const connection = {
  id: 1,
  provider: 'search-console',
  property: 'sc-domain:example.test',
  status: 'active' as const,
  synced_through: '2026-10-09',
  last_sync_at: '2026-10-11T08:00:00Z',
  created_at: '2026-10-01T00:00:00Z',
}

const top = {
  dimension: 'query',
  from: '2026-09-15',
  to: '2026-10-12',
  totals: { key: '', clicks: 30, impressions: 600, ctr: 0.05, position: 4 },
  rows: [
    { key: 'analytics tool', clicks: 10, impressions: 100, ctr: 0.1, position: 3 },
    { key: 'privacy analytics', clicks: 20, impressions: 500, ctr: 0.04, position: 5 },
  ],
}

function serve(entries: unknown[], rows = top) {
  request.mockImplementation(async (method, path) => {
    if (method === 'GET' && String(path).endsWith('/integrations')) return { data: entries }
    if (method === 'GET' && String(path).includes('/search-console/top')) return rows
    if (method === 'POST') return { authorization_url: 'https://accounts.example.test/auth?state=s' }
    return undefined
  })
}

function renderPage(role = 'owner', result?: IntegrationResult) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <IntegrationsPage orgId={3} result={result} role={role} siteId={7} />
    </QueryClientProvider>,
  )
}

describe('integrations pure helpers', () => {
  it('maps connection state', () => {
    expect(connectionState(undefined)).toBe('none')
    expect(connectionState(connection)).toBe('active')
    expect(connectionState({ ...connection, status: 'reauth' })).toBe('reauth')
    expect(connectionState({ ...connection, last_error: 'boom' })).toBe('error')
  })

  it('computes inclusive ranges, sorts and filters', () => {
    expect(rangeDates(7, new Date('2026-10-12T10:00:00Z'))).toEqual({ from: '2026-10-06', to: '2026-10-12' })
    expect(sortRows(top.rows, { key: 'position', direction: 'asc' }).map((row) => row.key)).toEqual(['analytics tool', 'privacy analytics'])
    expect(sortRows(top.rows, { key: 'clicks', direction: 'desc' })[0].key).toBe('privacy analytics')
    expect(filterRows(top.rows, ' PRIVACY ').map((row) => row.key)).toEqual(['privacy analytics'])
  })
})

describe('integrations page', () => {
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('explains when the instance has no provider configured', async () => {
    serve([])
    renderPage()
    expect(await screen.findByText('No integrations are configured on this instance')).toBeVisible()
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1)
  })

  it('starts the connection and sends the browser to the authorization URL', async () => {
    serve([{ provider: 'search-console', name: 'Google Search Console', available: true }])
    const assign = vi.fn()
    vi.stubGlobal('location', { ...window.location, assign })
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Connect with Google' }))
    expect(await screen.findByText('Enter the Search Console property to import.')).toBeVisible()
    fireEvent.change(screen.getByLabelText('Search Console property'), { target: { value: 'sc-domain:example.test' } })
    fireEvent.click(screen.getByRole('button', { name: 'Connect with Google' }))
    await waitFor(() => expect(assign).toHaveBeenCalledWith('https://accounts.example.test/auth?state=s'))
    expect(request).toHaveBeenCalledWith('POST', '/api/private/sites/7/integrations/search-console/connect', {
      orgId: 3,
      body: { property: 'sc-domain:example.test', backfill_days: 28 },
    })
    vi.unstubAllGlobals()
  })

  it('shows status and the report, and confirms disconnect with the data-deletion warning', async () => {
    serve([{ provider: 'search-console', name: 'Google Search Console', available: true, connection }])
    renderPage()
    expect(await screen.findByText('sc-domain:example.test')).toBeVisible()
    expect(screen.getByText('Connected')).toBeVisible()
    expect(await screen.findByText('privacy analytics')).toBeVisible()

    fireEvent.click(screen.getByRole('button', { name: /Disconnect/ }))
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText(/deletes the stored access tokens and every imported/)).toBeVisible()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete tokens and imported data' }))
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith('DELETE', '/api/private/sites/7/integrations/search-console', { orgId: 3 }),
    )
  })

  it('is read-only for non-managers', async () => {
    serve([{ provider: 'search-console', name: 'Google Search Console', available: true, connection }])
    renderPage('editor')
    expect(await screen.findByText(/Only owners and admins/)).toBeVisible()
    expect(screen.queryByRole('button', { name: /Disconnect/ })).toBeNull()
  })

  it('offers reconnecting when the grant needs reauthorization', async () => {
    serve([{ provider: 'search-console', name: 'Google Search Console', available: true, connection: { ...connection, status: 'reauth' } }])
    renderPage()
    expect(await screen.findByText('Reconnect needed')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Reconnect with Google' })).toBeVisible()
    expect(screen.getByLabelText('Search Console property')).toHaveValue('sc-domain:example.test')
  })

  it('shows the callback result message', async () => {
    serve([])
    renderPage('owner', 'denied')
    expect(await screen.findByRole('alert')).toHaveTextContent('Access was not granted')
  })

  it('sorts and filters the report with accessible sort state', async () => {
    serve([{ provider: 'search-console', name: 'Google Search Console', available: true, connection }])
    renderPage()
    await screen.findByText('privacy analytics')
    const body = () => screen.getAllByRole('row').slice(1).map((row) => within(row).getAllByRole('cell')[0].textContent)
    expect(body()).toEqual(['privacy analytics', 'analytics tool'])
    expect(screen.getByRole('cell', { name: '10%' })).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: /Avg. position/ }))
    expect(body()).toEqual(['analytics tool', 'privacy analytics'])
    expect(screen.getByRole('columnheader', { name: /Avg. position/ })).toHaveAttribute('aria-sort', 'ascending')
    fireEvent.change(screen.getByLabelText('Filter rows'), { target: { value: 'zzz' } })
    expect(screen.getByText('No rows match the filter.')).toBeVisible()
  })

  it('shows an empty state when there is no data', async () => {
    serve([{ provider: 'search-console', name: 'Google Search Console', available: true, connection }], { ...top, rows: [] })
    renderPage()
    expect(await screen.findByText(/No Search Console data in this range yet/)).toBeVisible()
  })
})
