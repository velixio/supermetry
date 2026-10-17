import '@testing-library/jest-dom/vitest'

import { act, cleanup, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/settings/privateApi')>()),
  privateRequest,
}))

import { DashboardNoDataBanner, NoDataBanner } from './NoDataBanner'
import { renderWithRouter } from '@/test/renderWithRouter'

afterEach(() => {
  cleanup()
  privateRequest.mockReset()
})

function renderBanner(client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  renderWithRouter(
    <QueryClientProvider client={client}>
      <DashboardNoDataBanner siteId={7} orgId={3} href="/app/acme/docs/settings/installation" />
    </QueryClientProvider>,
  )
  return client
}

describe('NoDataBanner', () => {
  it('links an eventless site to its installation steps without reading the arrow aloud', async () => {
    renderWithRouter(<NoDataBanner href="/app/acme/docs/settings/installation" />)
    const link = await screen.findByRole('link', { name: 'View installation steps' })
    expect(link).toHaveAttribute('href', '/app/acme/docs/settings/installation')
    expect(screen.getByText('No events yet. Finish installing the tracker to see data here.')).toBeVisible()
  })

  it('appears only while the site waits for its first event', async () => {
    privateRequest.mockResolvedValue({ install_status: 'quiet' })
    renderBanner()
    await waitFor(() => expect(privateRequest).toHaveBeenCalled())
    expect(screen.queryByRole('link', { name: 'View installation steps' })).not.toBeInTheDocument()
  })

  it('flips to a confirmation and refreshes the dashboard when the first event lands', async () => {
    privateRequest.mockResolvedValueOnce({ install_status: 'waiting' })
    const client = renderBanner()
    expect(await screen.findByRole('link', { name: 'View installation steps' })).toBeVisible()
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    privateRequest.mockResolvedValue({ install_status: 'receiving' })
    await act(async () => { await client.refetchQueries({ queryKey: ['tracking-status', 7] }) })
    expect(await screen.findByText('Your first event arrived. The dashboard is live.')).toBeVisible()
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['stats', 7] })
  })
})
