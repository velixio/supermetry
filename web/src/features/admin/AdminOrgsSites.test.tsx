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
const storage = {
  estimated_storage_bytes: 3_145_728,
  database_bytes: 2_097_152,
  replay_bytes: 1_048_576,
  measured_at: '2026-10-26T10:00:00Z',
  measurement_version: 1,
}
const deletedOrganization = {
  ...organization,
  deleted_at: '2026-10-26T10:00:00Z',
  restorable_until: '2026-11-02T10:00:00Z',
  deleted_by_kind: 'instance_admin' as const,
  deleted_by_user_id: 12,
  deleted_by_name: 'user12',
}
const site = {
  id: 11, org_id: 4, organization: 'Doomed Co', public_id: 'pub', name: 'Blog', retention_days: 365 as const, pageviews: 12,
  storage: { ...storage, hot_bytes: 1, retained_bytes: 2, auxiliary_bytes: 3, replay_recording_bytes: 4, replay_css_bytes: 5 },
}

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

  it('shows a deleted organization total and per-site storage breakdown with freshness', async () => {
    privateRequest.mockImplementation(async (method: string, path: string) => {
      if (method === 'GET' && path.startsWith('/api/private/admin/organizations?')) return { data: [{ ...deletedOrganization, storage }] }
      if (method === 'GET') return { ...deletedOrganization, storage, members: [], sites: [site] }
      return undefined
    })
    renderPage(<AdminOrganizationsPage />)

    const orgStorage = await screen.findByRole('button', { name: 'Doomed Co, 3 MiB. Show storage details' })
    expect(orgStorage).toHaveTextContent('3 MiB')
    fireEvent.click(orgStorage)
    expect(await screen.findByText('2 MiB')).toBeVisible()
    expect(screen.getByText('1 MiB')).toBeVisible()
    expect(screen.getByText(/Updated .*2026/)).toBeVisible()

    fireEvent.click(screen.getByRole('button', { name: 'Details: Doomed Co' }))
    const siteStorage = await screen.findByRole('button', { name: 'Blog, 3 MiB. Show storage details' })
    expect(siteStorage).toHaveTextContent('3 MiB')
  })

  it('does not render missing organization storage as zero', async () => {
    privateRequest.mockResolvedValue({ data: [{ ...organization, storage: null }] })
    renderPage(<AdminOrganizationsPage />)
    expect(await screen.findByText('Calculating…')).toBeVisible()
    expect(screen.queryByText('0 B')).not.toBeInTheDocument()
  })

  it('renders a measured zero organization snapshot as zero rather than calculating', async () => {
    privateRequest.mockResolvedValue({
      data: [{
        ...organization,
        storage: { ...storage, estimated_storage_bytes: 0, database_bytes: 0, replay_bytes: 0 },
      }],
    })
    renderPage(<AdminOrganizationsPage />)

    expect(await screen.findByRole('button', { name: 'Doomed Co, 0 B. Show storage details' })).toBeVisible()
    expect(screen.queryByText('Calculating…')).not.toBeInTheDocument()
  })
})

describe('admin sites', () => {
  it('shows total storage with an accessible database, replay, and freshness breakdown', async () => {
    privateRequest.mockResolvedValue({ data: [site] })
    renderPage(<AdminSitesPage />)

    const trigger = await screen.findByRole('button', { name: 'Blog, 3 MiB. Show storage details' })
    expect(trigger).toHaveTextContent('3 MiB')
    fireEvent.click(trigger)
    expect(await screen.findByText('Database')).toBeVisible()
    expect(screen.getByText('2 MiB')).toBeVisible()
    expect(screen.getByText('Replay')).toBeVisible()
    expect(screen.getByText('1 MiB')).toBeVisible()
    expect(screen.getByText(/Updated .*2026/)).toBeVisible()
  })

  it('does not render missing site storage as zero', async () => {
    privateRequest.mockResolvedValue({ data: [{ ...site, storage: null }] })
    renderPage(<AdminSitesPage />)
    expect(await screen.findByText('Calculating…')).toBeVisible()
    expect(screen.queryByText('0 B')).not.toBeInTheDocument()
  })

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
