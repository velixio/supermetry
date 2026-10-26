import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', async (original) => ({
  ...await original<typeof import('@/features/settings/privateApi')>(),
  privateRequest,
}))

import { AdminOrganizationsPage } from './AdminOrganizationsPage'
import { AdminSitesPage } from './AdminSitesPage'

const organization = { id: 4, name: 'Doomed Co', slug: 'doomed', member_count: 1, site_count: 1, site_limit: 10 }
const deletedOrganization = {
  ...organization,
  deleted_at: '2026-10-26T10:00:00Z',
  restorable_until: '2026-11-02T10:00:00Z',
  deleted_by_kind: 'instance_admin' as const,
  deleted_by_user_id: 12,
  deleted_by_name: 'user12',
}
const site = { id: 11, org_id: 4, organization: 'Doomed Co', public_id: 'pub', name: 'Blog', retention_days: 365 as const, pageviews: 12 }

function renderPage(node: ReactNode) {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>{node}</QueryClientProvider>)
}

beforeEach(() => {
  privateRequest.mockReset()
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ retention_forever: false, default_retention_days: 90 }), { status: 200 })))
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe('admin organizations', () => {
  it('moves focus to the details and deletes only after the name is typed', async () => {
    privateRequest.mockImplementation(async (method: string, path: string) => {
      if (method === 'GET' && path.startsWith('/api/private/admin/organizations?')) return { data: [organization] }
      if (method === 'GET') return { ...organization, members: [{ id: 1, email: 'o@example.test', name: 'O' }], sites: [site] }
      return undefined
    })
    renderPage(<AdminOrganizationsPage />)
    fireEvent.click(await screen.findByRole('button', { name: 'Details: Doomed Co' }))
    const heading = await screen.findByRole('heading', { name: /Doomed Co/ , level: 2 })
    await waitFor(() => expect(heading).toHaveFocus())
    expect(screen.getByText('No events yet')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Delete organization' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('restore it for 7 days')
    const confirm = within(dialog).getByRole('button', { name: 'Delete organization' })
    expect(confirm).toBeDisabled()
    fireEvent.change(within(dialog).getByLabelText('Type Doomed Co to confirm'), { target: { value: 'Doomed Co' } })
    fireEvent.click(confirm)
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('DELETE', '/api/private/admin/organizations/4'))
    expect(await screen.findByText(/Doomed Co was deleted/)).toBeVisible()
  })

  it('identifies an admin-deleted organization and lets only the admin restore it', async () => {
    privateRequest.mockImplementation(async (method: string, path: string) => {
      if (method === 'GET' && path.startsWith('/api/private/admin/organizations?')) return { data: [deletedOrganization] }
      if (method === 'GET') return { ...deletedOrganization, members: [{ id: 1, email: 'o@example.test', name: 'O' }], sites: [site] }
      return undefined
    })
    renderPage(<AdminOrganizationsPage />)

    expect(await screen.findByText('Deleted by admin / user12')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Details: Doomed Co' }))
    await waitFor(() => expect(screen.getAllByText('Deleted by admin / user12')).toHaveLength(2))
    expect(screen.queryByRole('button', { name: 'Delete organization' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Restore organization' }))

    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('POST', '/api/private/admin/organizations/4/restore'))
    expect(await screen.findByText('Doomed Co was restored.')).toBeVisible()
  })
})

describe('admin sites', () => {
  it('edits retention per row and confirms a reduction', async () => {
    privateRequest.mockImplementation(async (method: string) => (method === 'GET' ? { data: [site] } : { id: 11, retention_days: 30 }))
    renderPage(<AdminSitesPage />)
    const select = await screen.findByLabelText('Retention for Blog')
    await waitFor(() => expect(within(select).queryByRole('option', { name: 'Keep forever' })).not.toBeInTheDocument())
    fireEvent.change(select, { target: { value: '30' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save: Retention for Blog' }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Lower retention to 30 days?' })
    expect(privateRequest).not.toHaveBeenCalledWith('PATCH', expect.anything(), expect.anything())
    fireEvent.click(within(dialog).getByRole('button', { name: 'Lower retention' }))
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('PATCH', '/api/private/admin/sites/11', { body: { retention_days: 30 } }))
    expect(await screen.findByText('Retention updated for Blog.')).toBeVisible()
  })
})
