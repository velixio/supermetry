import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { getDeletedSites, restoreSite } from './api'
import { DeletedSitesSection } from './DeletedSitesSection'

vi.mock('./api', () => ({ getDeletedSites: vi.fn(), restoreSite: vi.fn() }))

afterEach(cleanup)

it('lists scheduled sites and restores one through the lifecycle API', async () => {
  vi.mocked(getDeletedSites).mockResolvedValue([{
    id: 7,
    name: 'Docs',
    deleted_at: '2026-10-11T08:00:00Z',
    purge_at: '2026-10-18T08:00:00Z',
  }])
  vi.mocked(restoreSite).mockResolvedValue(undefined)
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })

  render(
    <QueryClientProvider client={queryClient}>
      <DeletedSitesSection orgId={4} />
    </QueryClientProvider>,
  )

  expect(await screen.findByRole('heading', { name: 'Scheduled for deletion' })).toBeVisible()
  expect(screen.getByText('Docs')).toBeVisible()
  expect(screen.getByText(/Deletes on/)).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: 'Restore Docs' }))

  await waitFor(() => expect(restoreSite).toHaveBeenCalledWith(7, 4))
})

it.each([
  ['not_restorable', 'This can no longer be restored.'],
  ['site_limit', 'Your organization is at its site limit.'],
  ['unexpected', 'The site could not be restored.'],
])('shows %s restore errors inline', async (code, message) => {
  vi.mocked(getDeletedSites).mockResolvedValue([{ id: 7, name: 'Docs', deleted_at: '2026-10-11T08:00:00Z', purge_at: '2026-10-18T08:00:00Z' }])
  vi.mocked(restoreSite).mockRejectedValue(new Error(code))
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><DeletedSitesSection orgId={4} /></QueryClientProvider>)
  fireEvent.click(await screen.findByRole('button', { name: 'Restore Docs' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(message)
})
