import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { AppSettingsPage } from './AppSettingsPage'

function renderPage() {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <AppSettingsPage orgId={3} siteId={7} publicId="pub7" />
    </QueryClientProvider>,
  )
}

describe('AppSettingsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    privateRequest.mockImplementation((method: string, path: string, options?: { body?: unknown }) => {
      if (path.endsWith('/app-settings')) {
        return Promise.resolve(method === 'PUT'
          ? { tag_allowlist: ['beta', 'stable'], max_report_age_days: 14, environments: ['prod'] }
          : { tag_allowlist: ['a'], max_report_age_days: 7, environments: [] })
      }
      if (method === 'POST') return Promise.resolve({ key: 'smk_new', prefix: 'zzzz9999', previous_expires_at: '2026-11-22T00:00:00Z' })
      return Promise.resolve({ keys: [
        { prefix: 'abcd1234', created_at: '2026-11-01T00:00:00Z', expires_at: null },
        { prefix: 'old00000', created_at: '2026-10-01T00:00:00Z', expires_at: '2026-11-20T00:00:00Z' },
      ] })
    })
  })
  afterEach(cleanup)

  it('loads settings, saves parsed lists and shows the normalised response', async () => {
    renderPage()
    const tags = await screen.findByLabelText('Tag allowlist')
    await waitFor(() => expect(tags).toHaveValue('a'))
    fireEvent.change(tags, { target: { value: 'beta, stable\nbeta' } })
    fireEvent.change(screen.getByLabelText('Max report age (days)'), { target: { value: '14' } })
    fireEvent.change(screen.getByLabelText('Environments'), { target: { value: 'prod' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('PUT', '/api/private/sites/7/app-settings', {
      orgId: 3,
      body: { tag_allowlist: ['beta', 'stable'], max_report_age_days: 14, environments: ['prod'] },
    }))
    await waitFor(() => expect(screen.getByLabelText('Tag allowlist')).toHaveValue('beta\nstable'))
  })

  it('lists keys and rotates after confirmation', async () => {
    renderPage()
    expect(await screen.findByText('smk_abcd1234…')).toBeInTheDocument()
    expect(screen.getByText(/valid until/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Rotate key' }))
    expect(screen.getByText('The current key keeps working for 7 days.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Rotate' }))
    expect(await screen.findByDisplayValue('smk_new')).toBeInTheDocument()
    expect(screen.getByText(/SUPERMETRY_APP_ID=pub7/)).toBeInTheDocument()
  })
})
