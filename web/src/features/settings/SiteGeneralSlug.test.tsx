import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))

vi.mock('./privateApi', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./privateApi')>()),
  privateRequest,
}))

import { SiteGeneralPage } from './SettingsPages'
import { renderWithRouter } from '@/test/renderWithRouter'

const site = {
  id: 7,
  public_id: 'wekvsjfz6un3',
  slug: 'voicestudio-sh',
  name: 'Voice Studio',
  allowed_hosts: ['voicestudio.sh'],
  timezone: 'Asia/Kolkata',
  retention_days: 30,
  profiles_enabled: false,
  revenue_currency_state: 'configured',
  revenue_currency: 'USD',
}

describe('site URL name', () => {
  beforeEach(() => {
    privateRequest.mockReset()
    privateRequest.mockImplementation(async (method: string) => (method === 'GET' ? site : { ...site, slug: 'studio' }))
  })
  afterEach(cleanup)

  function renderPage(onSlugChange = vi.fn()) {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
    renderWithRouter(
      <QueryClientProvider client={queryClient}>
        <SiteGeneralPage orgId={3} role="owner" siteId={7} onSlugChange={onSlugChange} />
      </QueryClientProvider>,
    )
    return { onSlugChange, invalidate }
  }

  it('shows the current slug and saves a new one, then reports the change', async () => {
    const { onSlugChange, invalidate } = renderPage()
    const field = await screen.findByLabelText('URL name')
    expect(field).toHaveValue('voicestudio-sh')

    fireEvent.change(field, { target: { value: 'studio' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))

    await waitFor(() => expect(onSlugChange).toHaveBeenCalledWith('studio'))
    const patch = privateRequest.mock.calls.find(([method]) => method === 'PATCH')
    expect(patch?.[2].body).toMatchObject({ slug: 'studio', name: 'Voice Studio' })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['me'] })
  })

  it('stays on the page and shows the error when the slug is taken', async () => {
    privateRequest.mockImplementation(async (method: string) => {
      if (method === 'GET') return site
      throw new Error('site slug is already used')
    })
    const { onSlugChange } = renderPage()
    fireEvent.change(await screen.findByLabelText('URL name'), { target: { value: 'taken' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))

    expect(await screen.findByText(/site slug is already used/)).toBeVisible()
    expect(onSlugChange).not.toHaveBeenCalled()
  })

  it('does not report a change when other fields are saved', async () => {
    privateRequest.mockImplementation(async (method: string) => (method === 'GET' ? site : { ...site, name: 'Renamed' }))
    const { onSlugChange } = renderPage()
    fireEvent.change(await screen.findByLabelText('Name'), { target: { value: 'Renamed' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))

    await waitFor(() => expect(privateRequest.mock.calls.some(([method]) => method === 'PATCH')).toBe(true))
    await new Promise((resolve) => setTimeout(resolve, 20))
    expect(onSlugChange).not.toHaveBeenCalled()
  })
})

describe('site retention', () => {
  beforeEach(() => {
    privateRequest.mockReset()
    privateRequest.mockImplementation(async (method: string) => (method === 'GET' ? { ...site, retention_days: 365 } : { ...site, retention_days: 7 }))
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ signup: 'invite', setup_required: false, homepage_url: '/', default_retention_days: 90, retention_forever: false }), { status: 200 })))
  })
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  function renderPage() {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    renderWithRouter(
      <QueryClientProvider client={queryClient}>
        <SiteGeneralPage orgId={3} role="owner" siteId={7} />
      </QueryClientProvider>,
    )
  }

  it('confirms before lowering retention and states what is deleted', async () => {
    renderPage()
    const retention = await screen.findByLabelText('Raw data retention')
    expect(retention).toHaveValue('365')
    await waitFor(() => expect(screen.queryByRole('option', { name: 'Keep forever' })).not.toBeInTheDocument())
    fireEvent.change(retention, { target: { value: '7' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Lower retention to 7 days?' })
    expect(dialog).toHaveTextContent('Raw events older than 7 days will be permanently deleted')
    expect(privateRequest.mock.calls.some(([method]) => method === 'PATCH')).toBe(false)
    fireEvent.click(screen.getByRole('button', { name: 'Lower retention' }))
    await waitFor(() => expect(privateRequest.mock.calls.find(([method]) => method === 'PATCH')?.[2].body).toMatchObject({ retention_days: 7 }))
    expect(await screen.findByText('Site settings saved.')).toHaveAttribute('role', 'status')
  })

  it('saves a longer retention without asking', async () => {
    privateRequest.mockImplementation(async (method: string) => (method === 'GET' ? { ...site, retention_days: 30 } : { ...site, retention_days: 90 }))
    renderPage()
    fireEvent.change(await screen.findByLabelText('Raw data retention'), { target: { value: '90' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(privateRequest.mock.calls.some(([method]) => method === 'PATCH')).toBe(true))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })
})
