import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { listSitesOverview } from './api'
import { SitesOverview, type OverviewSite } from './SitesOverview'
import { renderWithRouter } from '@/test/renderWithRouter'

const site: OverviewSite = {
  id: 7,
  org_id: 3,
  public_id: 'docs-public',
  name: 'Docs',
  domain: 'docs.supermetry.com',
  timezone: 'Asia/Kathmandu',
  organization: { id: 3, name: 'Acme', slug: 'acme' },
  role: 'owner',
  created_at: '2026-10-01T08:00:00Z',
  visitors: 120,
  previous_visitors: 100,
  change_percent: 20,
  sparkline: [{ start: '2026-10-12T08:00:00Z', visitors: 4 }, { start: '2026-10-12T09:00:00Z', visitors: 9 }],
  live_visitors: 3,
  last_event_at: '2026-10-12T09:00:00Z',
  install_status: 'receiving',
  tracking_snippet: '<script data-site="docs-public"></script>',
}

function renderOverview(props: Partial<React.ComponentProps<typeof SitesOverview>> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return renderWithRouter(
    <QueryClientProvider client={client}>
      <SitesOverview
        canAdd
        orgs={[{ id: 3, name: 'Acme', slug: 'acme' }]}
        period="24h"
        query=""
        sort="visitors"
        view="cards"
        onSearchChange={vi.fn()}
        onPeriodChange={vi.fn()}
        onSortChange={vi.fn()}
        onOrgChange={vi.fn()}
        onViewChange={vi.fn()}
        {...props}
      />
    </QueryClientProvider>,
  )
}

describe('sites overview', () => {
  beforeEach(() => {
    privateRequest.mockReset()
    Element.prototype.scrollIntoView = vi.fn()
    Element.prototype.hasPointerCapture = vi.fn(() => false)
    Element.prototype.releasePointerCapture = vi.fn()
  })
  afterEach(cleanup)

  it('loads all readable sites through one bounded request', async () => {
    privateRequest.mockResolvedValue({ data: [site], next_cursor: null })
    await expect(listSitesOverview('24h')).resolves.toEqual({ data: [site], next_cursor: null })
    expect(privateRequest).toHaveBeenCalledWith('GET', '/api/private/sites/overview?period=24h&limit=200')
  })

  it('renders figures, an accessible sparkline, and keyboard actions', async () => {
    privateRequest.mockResolvedValue({ data: [site] })
    renderOverview()

    expect((await screen.findByRole('link', { name: /Docs.*120 visitors.*3 live/i })).getAttribute('href')).toMatch(/^\/app\/acme\/docs-public/)
    expect(screen.getByRole('img', { name: /Traffic for Docs/i })).toHaveAccessibleDescription(/Low 4.*peak 9/i)
    fireEvent.keyDown(screen.getByRole('button', { name: 'Actions for Docs' }), { key: 'ArrowDown' })
    expect(await screen.findByRole('menuitem', { name: 'Open dashboard' })).toBeVisible()
    expect(screen.getByText('Receiving')).toBeVisible()
  })

  it('links to the readable slug and still uses the public id when a site has none', async () => {
    privateRequest.mockResolvedValue({ data: [{ ...site, slug: 'docs-example-com' }, { ...site, id: 8, public_id: 'shop-public', name: 'Shop' }] })
    renderOverview()

    expect((await screen.findByRole('link', { name: /Docs.*120 visitors/i })).getAttribute('href')).toMatch(/^\/app\/acme\/docs-example-com/)
    expect(screen.getByRole('link', { name: /Shop.*120 visitors/i }).getAttribute('href')).toMatch(/^\/app\/acme\/shop-public/)
  })

  it('shows loading, error, and filtered-empty states', async () => {
    privateRequest.mockImplementation(() => new Promise(() => undefined))
    const loading = renderOverview()
    expect(await screen.findByRole('status', { name: 'Loading sites' })).toBeVisible()
    loading.unmount()

    privateRequest.mockRejectedValue(new Error('overview unavailable'))
    const error = renderOverview()
    expect(await screen.findByRole('alert')).toHaveTextContent('overview unavailable')
    error.unmount()

    privateRequest.mockResolvedValue({ data: [site] })
    renderOverview({ query: 'missing' })
    expect(await screen.findByText('No sites match these filters.')).toBeVisible()
  })

  it('switches view through the remembered-preference callback and handles 100 sites', async () => {
    const onViewChange = vi.fn()
    privateRequest.mockResolvedValue({ data: Array.from({ length: 100 }, (_, index) => ({ ...site, id: index + 1, public_id: `site-${index + 1}`, name: `Site ${index + 1}` })) })
    renderOverview({ onViewChange })
    expect(await screen.findAllByTestId('site-overview-card')).toHaveLength(100)
    fireEvent.click(screen.getByRole('radio', { name: 'Table view' }))
    expect(onViewChange).toHaveBeenCalledWith('table')
  })

  it('uses the chosen search, organization, period, and sort callbacks', async () => {
    privateRequest.mockResolvedValue({ data: [site] })
    const onSearchChange = vi.fn()
    const onPeriodChange = vi.fn()
    renderOverview({ onSearchChange, onPeriodChange })
    fireEvent.change(await screen.findByLabelText('Search sites'), { target: { value: 'docs' } })
    fireEvent.click(screen.getByLabelText('Period'))
    fireEvent.click(await screen.findByRole('option', { name: '7 days' }))
    expect(onSearchChange).toHaveBeenCalledWith('docs')
    expect(onPeriodChange).toHaveBeenCalledWith('7d')
  })
})
