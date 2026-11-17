import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { AppSettingsPage } from './AppSettingsPage'

function renderPage(role = 'owner') {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <AppSettingsPage orgId={3} siteId={7} publicId="pub7" role={role} />
    </QueryClientProvider>,
  )
}

const base = {
  tag_allowlist: [], max_report_age_days: 7, environments: [],
  filter_extensions: true, filter_crawlers: true, filter_localhost: null, filter_localhost_default: false,
}

describe('AppSettingsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    privateRequest.mockImplementation((method: string, path: string, options?: { body?: unknown }) => {
      if (path.endsWith('/app-settings')) {
        return Promise.resolve(method === 'PUT'
          ? { ...base, tag_allowlist: ['beta', 'stable'], max_report_age_days: 14, environments: ['prod'] }
          : { ...base, tag_allowlist: ['a'], max_report_age_days: 7, environments: [] })
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
      body: {
        tag_allowlist: ['beta', 'stable'], max_report_age_days: 14, environments: ['prod'],
        filter_extensions: true, filter_crawlers: true, filter_localhost: null,
      },
    }))
    await waitFor(() => expect(screen.getByLabelText('Tag allowlist')).toHaveValue('beta\nstable'))
  })

  it('saves the inbound filter switches; localhost stays on the server default until changed', async () => {
    renderPage()
    const extensions = await screen.findByRole('switch', { name: 'Browser extensions' })
    await waitFor(() => expect(extensions).toBeChecked())
    expect(screen.getByRole('switch', { name: 'Localhost' })).not.toBeChecked()
    expect(screen.getByText(/Server default: off/)).toBeInTheDocument()
    fireEvent.click(extensions)
    fireEvent.click(screen.getByRole('switch', { name: 'Crawlers' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('PUT', '/api/private/sites/7/app-settings', expect.objectContaining({
      body: expect.objectContaining({ filter_extensions: false, filter_crawlers: false, filter_localhost: null }),
    })))
    fireEvent.click(screen.getByRole('switch', { name: 'Localhost' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(privateRequest).toHaveBeenLastCalledWith('PUT', '/api/private/sites/7/app-settings', expect.objectContaining({
      body: expect.objectContaining({ filter_localhost: true }),
    })))
  })

  it('queues an erasure after confirmation, then shows pending progress and the final counts', async () => {
    let polls = 0
    privateRequest.mockImplementation((method: string, path: string) => {
      if (method === 'POST' && path.endsWith('/app/users/erase')) {
        return Promise.resolve({ id: 41, status: 'pending', bodies_deleted: 0, events_anonymized: 0, last_error: null })
      }
      if (path.endsWith('/app/users/erasures/41')) {
        polls += 1
        return Promise.resolve(polls < 2
          ? { id: 41, status: 'running', bodies_deleted: 1, events_anonymized: 0, last_error: null }
          : { id: 41, status: 'done', bodies_deleted: 3, events_anonymized: 1, last_error: null })
      }
      if (path.endsWith('/app-settings')) return Promise.resolve(base)
      return Promise.resolve({ keys: [] })
    })
    renderPage()
    const erase = await screen.findByRole('button', { name: 'Erase data' })
    expect(erase).toBeDisabled()
    fireEvent.change(screen.getByLabelText('User id'), { target: { value: ' user-9 ' } })
    fireEvent.click(erase)
    expect(privateRequest).not.toHaveBeenCalledWith('POST', expect.anything(), expect.anything())
    fireEvent.click(await screen.findByRole('button', { name: 'Erase' }))
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('POST', '/api/private/sites/7/app/users/erase', { orgId: 3, body: { user_id: 'user-9' } }))
    expect(await screen.findByRole('status')).toHaveTextContent('Erasing in the background.')
    expect(screen.getByRole('button', { name: 'Erase data' })).toBeDisabled()
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Erased 3 stored event bodies and anonymized 1 event.'), { timeout: 6000 })
    expect(privateRequest).toHaveBeenCalledWith('GET', '/api/private/sites/7/app/users/erasures/41', { orgId: 3 })
    expect(screen.getByRole('button', { name: 'Erase data' })).toBeEnabled()
  })

  it('reports a failed erasure', async () => {
    privateRequest.mockImplementation((method: string, path: string) => {
      if (method === 'POST' && path.endsWith('/app/users/erase')) {
        return Promise.resolve({ id: 42, status: 'failed', bodies_deleted: 0, events_anonymized: 0, last_error: 'boom' })
      }
      if (path.endsWith('/app-settings')) return Promise.resolve(base)
      return Promise.resolve({ keys: [] })
    })
    renderPage()
    fireEvent.change(await screen.findByLabelText('User id'), { target: { value: 'u' } })
    fireEvent.click(screen.getByRole('button', { name: 'Erase data' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Erase' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Erasure failed: boom')
  })

  it('hides erasure from viewers', async () => {
    renderPage('viewer')
    await screen.findByLabelText('Tag allowlist')
    expect(screen.queryByRole('button', { name: 'Erase data' })).toBeNull()
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
