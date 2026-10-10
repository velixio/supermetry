import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
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
  it('submits name, domains, time zone, and retention', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    render(<SiteForm submitLabel="Add site" onSubmit={onSubmit} />)

    fireEvent.change(screen.getByLabelText('Site name'), { target: { value: 'Shop' } })
    fireEvent.change(screen.getByLabelText('Domains'), {
      target: { value: 'shop.example.com, checkout.example.com' },
    })
    fireEvent.change(screen.getByLabelText('Time zone'), { target: { value: 'Asia/Kathmandu' } })
    fireEvent.change(screen.getByLabelText('Retention'), { target: { value: '365' } })
    fireEvent.change(screen.getByLabelText('Revenue currency'), { target: { value: 'eur' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add site' }))

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith({
      name: 'Shop',
      hosts: ['shop.example.com', 'checkout.example.com'],
      timezone: 'Asia/Kathmandu',
      retention_days: 365,
      revenue_currency: 'EUR',
    }))
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
})
