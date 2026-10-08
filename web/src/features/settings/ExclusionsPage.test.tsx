import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { get, patch } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn() }))

vi.mock('@/lib/api', () => ({ api: { GET: get, PATCH: patch } }))

import { ExclusionsPage } from './ExclusionsPage'

function renderPage(role: 'owner' | 'admin' | 'editor' | 'viewer' = 'owner') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <ExclusionsPage orgId={3} role={role} siteId={7} />
    </QueryClientProvider>,
  )
  return client
}

describe('ExclusionsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    get.mockImplementation((path: string) => {
      if (path === '/api/private/me/ip') {
        return Promise.resolve({ data: { ip: '198.51.100.7' }, response: { ok: true, status: 200 } })
      }
      return Promise.resolve({
        data: {
          id: 7,
          exclude_ips: ['203.0.113.0/24'],
          exclude_paths: ['/admin/*', '/internal'],
          exclude_countries: ['RU'],
          heatmaps_enabled: false,
        },
        response: { ok: true, status: 200 },
      })
    })
    patch.mockResolvedValue({
      data: {
        id: 7,
        exclude_ips: ['203.0.113.0/24', '198.51.100.7/32'],
        exclude_paths: ['/preview/**', '/internal'],
        exclude_countries: ['RU'],
        heatmaps_enabled: false,
      },
      response: { ok: true, status: 200 },
    })
  })

  afterEach(cleanup)

  it('loads rules, adds the current IP, and saves trimmed replacement arrays', async () => {
    const client = renderPage()
    const ips = await screen.findByLabelText('IP addresses and ranges')
    const paths = screen.getByLabelText('Page paths')
    expect(ips).toHaveValue('203.0.113.0/24')
    expect(paths).toHaveValue('/admin/*\n/internal')

    fireEvent.click(screen.getByRole('button', { name: 'Add my current IP' }))
    await waitFor(() => expect(ips).toHaveValue('203.0.113.0/24\n198.51.100.7'))
    fireEvent.change(paths, { target: { value: ' /preview/** \n\n/internal ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save exclusions' }))

    await waitFor(() => expect(patch).toHaveBeenCalledWith(
      '/api/private/sites/7',
      expect.objectContaining({
        body: {
          exclude_ips: ['203.0.113.0/24', '198.51.100.7'],
          exclude_paths: ['/preview/**', '/internal'],
          exclude_countries: ['RU'],
        },
      }),
    ))
    expect(client.getQueryData(['site', 7])).toEqual(expect.objectContaining({
      exclude_ips: ['203.0.113.0/24', '198.51.100.7/32'],
    }))
  })

  it('shows the API validation detail inline', async () => {
    patch.mockResolvedValue({
      error: { detail: 'invalid exclude_ips entry "not-an-ip"' },
      response: { ok: false, status: 400 },
    })
    renderPage()
    const ips = await screen.findByLabelText('IP addresses and ranges')
    fireEvent.change(ips, { target: { value: 'not-an-ip' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save exclusions' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('invalid exclude_ips entry "not-an-ip"')
  })

  it('lets an owner enable heatmaps with a dedicated audited patch', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <ExclusionsPage orgId={3} role="owner" siteId={7} canManageHeatmaps />
      </QueryClientProvider>,
    )
    const toggle = await screen.findByRole('switch', { name: 'Collect click heatmaps' })
    await waitFor(() => expect(toggle).toBeEnabled())
    expect(toggle).not.toBeChecked()
    fireEvent.pointerDown(toggle, { button: 0, ctrlKey: false })
    fireEvent.click(toggle)
    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith(
        '/api/private/sites/7',
        expect.objectContaining({ body: { heatmaps_enabled: true } }),
      ),
    )
  })

  it('searches, adds, removes, and saves blocked countries', async () => {
    renderPage()

    expect(await screen.findByText('Russia')).toBeVisible()
    const search = screen.getByRole('searchbox', { name: 'Find a country' })
    fireEvent.change(search, { target: { value: 'India' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add India' }))
    fireEvent.click(screen.getByRole('button', { name: 'Remove Russia' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save exclusions' }))

    await waitFor(() => expect(patch).toHaveBeenCalledWith(
      '/api/private/sites/7',
      expect.objectContaining({
        body: expect.objectContaining({ exclude_countries: ['IN'] }),
      }),
    ))
  })

  it('hides blocked countries from editors and viewers', async () => {
    const { rerender } = render(
      <QueryClientProvider client={new QueryClient()}>
        <ExclusionsPage orgId={3} role="editor" siteId={7} />
      </QueryClientProvider>,
    )
    expect(screen.queryByLabelText('Blocked countries')).not.toBeInTheDocument()

    rerender(
      <QueryClientProvider client={new QueryClient()}>
        <ExclusionsPage orgId={3} role="viewer" siteId={7} />
      </QueryClientProvider>,
    )
    expect(screen.queryByLabelText('Blocked countries')).not.toBeInTheDocument()
  })

  it('shows country validation errors from the server', async () => {
    patch.mockResolvedValue({
      error: { detail: 'exclude_countries must contain at most 50 entries' },
      response: { ok: false, status: 400 },
    })
    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: 'Save exclusions' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('exclude_countries must contain at most 50 entries')
  })
})
