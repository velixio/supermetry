import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

const { del, get, post } = vi.hoisted(() => ({
  del: vi.fn(),
  get: vi.fn(),
  post: vi.fn(),
}))

vi.mock('@/lib/api', () => ({ api: { DELETE: del, GET: get, POST: post } }))

import { ApiKeysPage } from './SettingsPages'

beforeAll(() => {
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    unobserve() {}
    disconnect() {}
  })
  HTMLElement.prototype.scrollIntoView = vi.fn()
})

const sites = [
  { id: 10, name: 'Marketing' },
  { id: 20, name: 'Docs' },
]

function renderPage(role: 'owner' | 'admin' | 'editor' | 'viewer' = 'owner') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <ApiKeysPage orgId={3} orgSlug="acme" role={role} sites={sites} />
    </QueryClientProvider>,
  )
}

function openCreateDialog() {
  fireEvent.click(screen.getByRole('button', { name: 'Create key' }))
  return screen.getByRole('dialog', { name: 'Create API key' })
}

function fillName(dialog: HTMLElement, value = 'Automation') {
  fireEvent.change(within(dialog).getByLabelText('Key name'), { target: { value } })
}

describe('ApiKeysPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.setSystemTime(new Date('2026-10-08T12:00:00Z'))
    get.mockResolvedValue({ data: { data: [] }, response: { ok: true, status: 200 } })
    post.mockResolvedValue({
      data: { id: 7, key: 'pnl_example_secret' },
      response: { ok: true, status: 201 },
    })
    del.mockResolvedValue({ data: undefined, response: { ok: true, status: 204 } })
  })

  afterEach(() => {
    vi.useRealTimers()
    cleanup()
  })

  it('checks only stats:read by default', () => {
    renderPage()
    const dialog = openCreateDialog()

    expect(within(dialog).getByRole('checkbox', { name: /read analytics/i })).toBeChecked()
    expect(within(dialog).getByRole('checkbox', { name: /manage sites/i })).not.toBeChecked()
    expect(within(dialog).getByRole('checkbox', { name: /manage goals/i })).not.toBeChecked()
    for (const name of [/read profiles/i, /watch replays/i, /export data/i]) {
      expect(within(dialog).getByRole('checkbox', { name })).not.toBeChecked()
    }
    expect(within(dialog).getByText(/does not grant profiles, replay recordings, or downloads/i)).toBeVisible()
  })

  it.each(['editor', 'viewer'] as const)('hides API key controls from %ss', (role) => {
    renderPage(role)

    expect(screen.getByRole('alert')).toHaveTextContent('Only owners and admins can manage API keys')
    expect(screen.queryByRole('button', { name: 'Create key' })).not.toBeInTheDocument()
    expect(screen.queryByRole('checkbox', { name: /manage api keys/i })).not.toBeInTheDocument()
    expect(get).not.toHaveBeenCalled()
  })

  it('shows keys:manage with its warning to admins', () => {
    renderPage('admin')
    const dialog = openCreateDialog()

    expect(within(dialog).getByRole('checkbox', { name: /manage api keys/i })).toBeVisible()
    expect(within(dialog).getByText(/can create and revoke API keys/i)).toBeVisible()
  })

  it('sends selected site IDs for a restricted key', async () => {
    renderPage()
    const dialog = openCreateDialog()
    fillName(dialog)
    fireEvent.click(within(dialog).getByRole('combobox', { name: 'Site access' }))
    fireEvent.click(screen.getByRole('option', { name: 'Only these sites' }))
    fireEvent.click(within(dialog).getByRole('checkbox', { name: 'Docs' }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create key' }))

    await waitFor(() => expect(post).toHaveBeenCalledWith(
      '/api/private/orgs/3/api-keys',
      expect.objectContaining({ body: expect.objectContaining({ site_ids: [20] }) }),
    ))
  })

  it('sends a future expiry as expires_at', async () => {
    renderPage()
    const dialog = openCreateDialog()
    fillName(dialog)
    fireEvent.change(within(dialog).getByLabelText('Expiry date'), {
      target: { value: '2026-10-20' },
    })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create key' }))

    await waitFor(() => expect(post).toHaveBeenCalledWith(
      '/api/private/orgs/3/api-keys',
      expect.objectContaining({
        body: expect.objectContaining({ expires_at: '2026-10-20T00:00:00.000Z' }),
      }),
    ))
  })

  it('rejects an expiry date that is not in the future', () => {
    renderPage()
    const dialog = openCreateDialog()
    fillName(dialog)
    fireEvent.change(within(dialog).getByLabelText('Expiry date'), {
      target: { value: '2026-10-08' },
    })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create key' }))

    expect(within(dialog).getByRole('alert')).toHaveTextContent('Expiry date must be in the future')
    expect(post).not.toHaveBeenCalled()
  })

  it('shows server validation errors inside the create dialog', async () => {
    post.mockResolvedValue({
      data: undefined,
      error: { detail: 'This key name is already in use' },
      response: { ok: false, status: 409 },
    })
    renderPage()
    const dialog = openCreateDialog()
    fillName(dialog)
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create key' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent('This key name is already in use')
  })

  it('shows the created secret once with a ready-to-run curl example', async () => {
    renderPage()
    const dialog = openCreateDialog()
    fillName(dialog)
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create key' }))

    const secretDialog = await screen.findByRole('dialog', { name: 'Copy your API key' })
    expect(within(secretDialog).getByDisplayValue('pnl_example_secret')).toBeVisible()
    expect(within(secretDialog).getByText(/you won't see this key again/i)).toBeVisible()
    expect(within(secretDialog).getByText(/curl .*\/api\/public\/v1\/sites/s)).toHaveTextContent(
      'Authorization: Bearer pnl_example_secret',
    )

    fireEvent.click(within(secretDialog).getByRole('button', { name: 'Close' }))
    expect(screen.queryByDisplayValue('pnl_example_secret')).not.toBeInTheDocument()
  })

  it('requires confirmation before revoking a key', async () => {
    get.mockResolvedValue({
      data: { data: [{ id: 9, name: 'Deploy', prefix: 'pnl_abcd', scopes: ['stats:read'], site_ids: null, created_at: '2026-10-01T00:00:00Z', expires_at: null, last_used_at: null, revoked: false }] },
      response: { ok: true, status: 200 },
    })
    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: 'Revoke Deploy' }))
    expect(del).not.toHaveBeenCalled()
    const confirm = screen.getByRole('alertdialog', { name: 'Revoke API key?' })
    fireEvent.click(within(confirm).getByRole('button', { name: 'Revoke key' }))

    await waitFor(() => expect(del).toHaveBeenCalledWith(
      '/api/private/orgs/3/api-keys/9',
      expect.any(Object),
    ))
  })

  it('renders scopes, site names, dates, usage, and every key status', async () => {
    get.mockResolvedValue({
      data: { data: [
        { id: 1, name: 'Active', prefix: 'pnl_active', scopes: ['stats:read', 'goals:write'], site_ids: null, created_at: '2026-10-01T00:00:00Z', expires_at: null, last_used_at: '2026-10-08T10:00:00Z', revoked: false },
        { id: 2, name: 'Expired', prefix: 'pnl_expired', scopes: ['sites:write'], site_ids: [10, 20], created_at: '2026-09-01T00:00:00Z', expires_at: '2026-10-07T00:00:00Z', last_used_at: null, revoked: false },
        { id: 3, name: 'Revoked', prefix: 'pnl_revoked', scopes: ['annotations:write'], site_ids: [20], created_at: '2026-08-01T00:00:00Z', expires_at: '2027-01-01T00:00:00Z', last_used_at: null, revoked: true },
      ] },
      response: { ok: true, status: 200 },
    })
    renderPage()

    const table = await screen.findByRole('table', { name: 'API keys' })
    const rows = within(table).getAllByRole('row')
    expect(rows).toHaveLength(4)
    expect(rows[1]).toHaveTextContent('Activepnl_activestats:readgoals:writeAll sitesOct 1, 2026Never2 hours agoActive')
    expect(rows[2]).toHaveTextContent('Expiredpnl_expiredsites:writeMarketing, DocsSep 1, 2026Oct 7, 2026NeverExpired')
    expect(rows[3]).toHaveTextContent('Revokedpnl_revokedannotations:writeDocsAug 1, 2026Jan 1, 2027NeverRevoked')
  })

  it('links to the API reference and authentication guide', () => {
    renderPage()

    expect(screen.getByRole('link', { name: 'API reference' })).toHaveAttribute('href', '/app/acme/settings/api')
    expect(screen.getByRole('link', { name: 'API authentication guide' })).toHaveAttribute('href', '/docs/api-auth')
  })
})
