import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

import { SiteForm } from './SiteForm'
import { SitesPage, siteActions } from './SitesPage'
import { renderWithRouter } from '@/test/renderWithRouter'

afterEach(cleanup)

function renderSites(node: ReactNode) {
  renderWithRouter(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      {node}
    </QueryClientProvider>,
  )
}

describe('SiteForm', () => {
  beforeAll(() => {
    Element.prototype.scrollIntoView ??= vi.fn()
  })

  it('needs only a domain and a time zone, deriving the name and using the default retention', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    render(<SiteForm submitLabel="Add site" onSubmit={onSubmit} retention={{ defaultDays: 90, forever: false }} />)

    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'https://www.shop.example.com/' } })
    fireEvent.click(screen.getByRole('combobox', { name: 'Time zone' }))
    fireEvent.change(await screen.findByLabelText('Search time zones…'), { target: { value: 'tokyo' } })
    fireEvent.click(await screen.findByRole('option', { name: 'Asia/Tokyo' }))
    expect(screen.getByRole('combobox', { name: 'Time zone' })).toHaveTextContent('Asia/Tokyo')
    fireEvent.click(screen.getByRole('button', { name: 'Add site' }))

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith({
      name: 'shop.example.com',
      hosts: ['www.shop.example.com'],
      timezone: 'Asia/Tokyo',
    }))
    // The server applies the instance default retention when the user did not choose one.
    expect(onSubmit.mock.calls[0][0]).not.toHaveProperty('retention_days')
  })

  it('keeps retention and the permanent currency under advanced options', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    render(<SiteForm submitLabel="Add site" onSubmit={onSubmit} retention={{ defaultDays: 90, forever: false }} />)
    const retention = screen.getByLabelText('Raw data retention')
    expect(retention).toHaveValue('90')
    expect([...(retention as HTMLSelectElement).options].map((option) => option.value)).toEqual(['7', '30', '90', '365'])
    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'shop.example.com, checkout.example.com' } })
    fireEvent.change(retention, { target: { value: '365' } })
    fireEvent.change(screen.getByLabelText('Revenue currency (optional)'), { target: { value: 'eur' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add site' }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({
      hosts: ['shop.example.com', 'checkout.example.com'],
      retention_days: 365,
      revenue_currency: 'EUR',
    })))
  })

  it('shows the loaded default and hides keep forever until the settings allow it', () => {
    const onSubmit = vi.fn()
    const { rerender } = render(<SiteForm submitLabel="Add site" onSubmit={onSubmit} />)
    expect(screen.queryByRole('option', { name: 'Keep forever' })).not.toBeInTheDocument()
    rerender(<SiteForm submitLabel="Add site" onSubmit={onSubmit} retention={{ defaultDays: 90, forever: false }} />)
    expect(screen.getByLabelText('Raw data retention')).toHaveValue('90')
    expect(screen.queryByRole('option', { name: 'Keep forever' })).not.toBeInTheDocument()
  })

  it('offers keep forever only when the instance allows it', () => {
    render(<SiteForm submitLabel="Add site" onSubmit={vi.fn()} retention={{ defaultDays: 30, forever: true }} />)
    expect(screen.getByRole('option', { name: 'Keep forever' })).toBeInTheDocument()
  })

  it('shows a server error next to the field it concerns and focuses it', async () => {
    const onSubmit = vi.fn().mockRejectedValue(new Error('revenue currency must be three uppercase letters'))
    render(<SiteForm submitLabel="Add site" onSubmit={onSubmit} />)
    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'shop.example.com' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add site' }))
    const currency = screen.getByLabelText('Revenue currency (optional)')
    await waitFor(() => expect(currency).toHaveAttribute('aria-invalid', 'true'))
    expect(screen.getByLabelText('Domain')).not.toHaveAttribute('aria-invalid')
    await waitFor(() => expect(currency).toHaveFocus())
  })
})

describe('SitesPage', () => {
  const site = {
    id: 7,
    name: 'Docs',
    public_id: 'docs-public',
    timezone: 'UTC',
  }

  it('filters actions using the organization role', () => {
    expect(siteActions('owner')).toEqual(['edit', 'installation', 'delete'])
    expect(siteActions('editor')).toEqual(['edit', 'installation'])
    expect(siteActions('viewer')).toEqual(['installation'])
  })

  it('confirms deletion before calling the private API', async () => {
    const onDelete = vi.fn().mockResolvedValue(undefined)
    renderSites(<SitesPage org="acme" role="owner" sites={[site]} onDelete={onDelete} />)

    fireEvent.click(await screen.findByRole('button', { name: 'Delete Docs' }))
    expect(screen.getByRole('alertdialog')).toBeVisible()
    expect(onDelete).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Delete site' }))
    await waitFor(() => expect(onDelete).toHaveBeenCalledWith(7))
  })

  it.each([
    [false, 'The site stops collecting data now. You can restore it for 7 days; after that its data is permanently deleted.'],
    [true, 'The demo project and its data are deleted now.'],
  ])('shows the correct deletion consequence when demo is %s', async (isDemo, copy) => {
    renderSites(
      <SitesPage org="acme" role="owner" sites={[{ ...site, is_demo: isDemo }]} onDelete={vi.fn()} />,
    )
    fireEvent.click(await screen.findByRole('button', { name: 'Delete Docs' }))
    expect(screen.getByText(copy)).toBeVisible()
  })

  it('guides an empty organization to add its first site', async () => {
    renderSites(<SitesPage org="acme" role="owner" sites={[]} onDelete={vi.fn()} />)
    expect(await screen.findByRole('link', { name: 'Add your first site' })).toHaveAttribute(
      'href',
      '/app/acme/sites/new',
    )
  })

  it.each([
    ['waiting', undefined, 'Waiting'],
    ['receiving', '2026-10-12T09:00:00Z', 'Receiving'],
    ['quiet', '2026-10-10T09:00:00Z', 'Quiet'],
  ] as const)('shows %s tracker status', async (installStatus, lastSeenAt, label) => {
    renderSites(
      <SitesPage
        org="acme"
        role="viewer"
        sites={[{ ...site, install_status: installStatus, last_seen_at: lastSeenAt }]}
        onDelete={vi.fn()}
      />,
    )
    expect(await screen.findByText(label)).toBeVisible()
  })
})
