import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { getDeletedOrganizations, restoreOrganization } from './api'
import { DeletedOrgsList } from './DeletedOrgsList'

vi.mock('./api', () => ({ getDeletedOrganizations: vi.fn(), restoreOrganization: vi.fn() }))

afterEach(cleanup)

it('restores a scheduled organization and opens it', async () => {
  vi.mocked(getDeletedOrganizations).mockResolvedValue([{
    id: 4,
    name: 'Acme',
    slug: 'acme',
    deleted_at: '2026-10-11T08:00:00Z',
    purge_at: '2026-10-18T08:00:00Z',
    sites: ['Docs'],
  }])
  vi.mocked(restoreOrganization).mockResolvedValue(undefined)
  const navigate = vi.fn()
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  queryClient.setQueryData(['me'], { memberships: [] })

  render(
    <QueryClientProvider client={queryClient}>
      <DeletedOrgsList navigate={navigate} />
    </QueryClientProvider>,
  )

  expect(await screen.findByRole('heading', { name: 'Organizations scheduled for deletion' })).toBeVisible()
  expect(await screen.findByText(/Projects: Docs/)).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: 'Restore Acme' }))

  await waitFor(() => expect(restoreOrganization).toHaveBeenCalledWith(4))
  expect(navigate).toHaveBeenCalledWith('/app/acme/settings')
  expect(queryClient.getQueryState(['me'])?.isInvalidated).toBe(true)
})

it.each([
  ['not_restorable', 'This can no longer be restored.'],
  ['site_limit', 'Your organization is at its site limit.'],
  ['unexpected', 'The organization could not be restored.'],
])('shows %s restore errors inline', async (code, message) => {
  vi.mocked(getDeletedOrganizations).mockResolvedValue([{ id: 4, name: 'Acme', slug: 'acme', deleted_at: '2026-10-11T08:00:00Z', purge_at: '2026-10-18T08:00:00Z', sites: ['Docs'] }])
  vi.mocked(restoreOrganization).mockRejectedValue(new Error(code))
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><DeletedOrgsList navigate={vi.fn()} /></QueryClientProvider>)
  fireEvent.click(await screen.findByRole('button', { name: 'Restore Acme' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(message)
})
