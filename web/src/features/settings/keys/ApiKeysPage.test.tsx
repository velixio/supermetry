import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

const { del, get, post } = vi.hoisted(() => ({ del: vi.fn(), get: vi.fn(), post: vi.fn() }))
vi.mock('@/lib/api', () => ({ api: { DELETE: del, GET: get, POST: post } }))

import { ApiKeysPage } from './ApiKeysPage'

beforeAll(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  HTMLElement.prototype.scrollIntoView = vi.fn()
  HTMLElement.prototype.hasPointerCapture = vi.fn(() => false)
})

const sites = [{ id: 10, name: 'Marketing' }, { id: 20, name: 'Docs' }]
const keys = [
  { id: 1, name: 'claude', prefix: 'ygviw4h4', scopes: ['stats:read', 'profiles:read', 'data:export'], site_ids: [10], created_at: '2026-11-04T00:00:00Z', expires_at: '2027-02-02T00:00:00Z', last_used_at: '2026-11-04T10:00:00Z', last_used_country: 'DE', last_used_client: 'mcp', revoked: false, never_expires: false, expiring_soon: false },
  { id: 2, name: 'old ci', prefix: 'abcd1234', scopes: ['stats:read'], site_ids: null, created_at: '2026-01-01T00:00:00Z', expires_at: null, last_used_at: null, revoked: false, never_expires: true },
  { id: 3, name: 'gone', prefix: 'zzzz', scopes: ['stats:read'], site_ids: null, created_at: '2026-01-01T00:00:00Z', expires_at: '2027-01-01T00:00:00Z', last_used_at: null, revoked: true },
]

function renderPage(props: Partial<Parameters<typeof ApiKeysPage>[0]> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><ApiKeysPage orgId={3} role="owner" sites={sites} {...props} /></QueryClientProvider>)
}

const openMenu = (name: string) => fireEvent.pointerDown(screen.getByRole('button', { name: `Actions for ${name}` }), { button: 0, ctrlKey: false, pointerType: 'mouse' })

describe('ApiKeysPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.setSystemTime(new Date('2026-11-18T12:00:00Z'))
    get.mockResolvedValue({ data: { data: keys }, response: { ok: true, status: 200 } })
    post.mockResolvedValue({ data: { id: 9, key: 'pnl_new', expires_at: null, old_key_expires_at: '2026-11-19T12:00:00Z' }, response: { ok: true, status: 201 } })
    del.mockResolvedValue({ data: undefined, response: { ok: true, status: 204 } })
  })
  afterEach(() => { vi.useRealTimers(); cleanup() })

  it('shows only the slim columns, status chips and the unused flag', async () => {
    renderPage()
    const table = await screen.findByRole('table', { name: 'API keys' })
    expect(within(table).getAllByRole('columnheader').map((c) => c.textContent)).toEqual(['Name', 'Access', 'Last used', 'Expires', 'Actions'])
    const rows = within(table).getAllByRole('row')
    expect(rows[1]).toHaveTextContent('1 project')
    expect(rows[1]).toHaveTextContent('Active')
    expect(rows[2]).toHaveTextContent('No expiry')
    expect(rows[2]).toHaveTextContent('Unused 90 days')
    expect(rows[3]).toHaveTextContent('Revoked')
    // last-used details are available to assistive tech and the tooltip
    expect(rows[1]).toHaveTextContent('Client: MCP')
    expect(rows[1]).toHaveTextContent('Country: DE')
  })

  it('has one Create key button and no duplicate API reference link', async () => {
    renderPage()
    await screen.findByRole('table')
    expect(screen.getAllByRole('button', { name: 'Create key' })).toHaveLength(1)
    expect(screen.queryByRole('link', { name: /API reference/ })).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 1, name: 'API keys' })).toBeVisible()
  })

  it('hides controls from editors', () => {
    renderPage({ role: 'editor' })
    expect(screen.getByRole('alert')).toHaveTextContent('Only owners and admins can manage API keys')
    expect(get).not.toHaveBeenCalled()
  })

  it('opens the details drawer with prefix and full scopes', async () => {
    renderPage()
    await screen.findByRole('table')
    openMenu('claude')
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Details' }))
    const drawer = await screen.findByRole('dialog', { name: 'claude' })
    expect(drawer).toHaveTextContent('ygviw4h4')
    expect(drawer).toHaveTextContent('profiles:read')
    expect(drawer).toHaveTextContent('Marketing')
  })

  it('names the key in the revoke confirmation and deletes it', async () => {
    renderPage()
    await screen.findByRole('table')
    openMenu('claude')
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Revoke' }))
    const confirm = await screen.findByRole('alertdialog', { name: 'Revoke claude?' })
    expect(del).not.toHaveBeenCalled()
    fireEvent.click(within(confirm).getByRole('button', { name: 'Revoke key' }))
    await waitFor(() => expect(del).toHaveBeenCalledWith('/api/private/orgs/3/api-keys/1', expect.any(Object)))
  })

  it('rolls a key with the chosen grace period and shows the new secret once', async () => {
    renderPage()
    await screen.findByRole('table')
    openMenu('claude')
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Roll key' }))
    const dialog = await screen.findByRole('dialog', { name: 'Roll claude' })
    fireEvent.click(within(dialog).getByRole('combobox', { name: /Keep old key for/ }))
    fireEvent.click(await screen.findByRole('option', { name: '1 hour' }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Roll key' }))
    await waitFor(() => expect(post).toHaveBeenCalledWith('/api/private/orgs/3/api-keys/1/roll', expect.objectContaining({ body: { grace_hours: 1 } })))
    expect(await within(dialog).findByDisplayValue('pnl_new')).toBeVisible()
  })

  it('opens the wizard when create=1 was requested and reports close', async () => {
    const onClosed = vi.fn()
    renderPage({ createRequested: true, onCreateClosed: onClosed })
    const dialog = await screen.findByRole('dialog', { name: 'Create API key' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(onClosed).toHaveBeenCalled()
  })
})
