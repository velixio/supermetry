import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))
vi.mock('@/lib/api', () => ({ api: { POST: post, GET: vi.fn(), DELETE: vi.fn() } }))

import { CreateKeyWizard } from './CreateKeyWizard'

beforeAll(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  HTMLElement.prototype.scrollIntoView = vi.fn()
  HTMLElement.prototype.hasPointerCapture = vi.fn(() => false)
})

const sites = [{ id: 10, name: 'Marketing' }, { id: 20, name: 'Docs' }]

function renderWizard(initialPurpose?: 'mcp') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const onOpenChange = vi.fn()
  render(<QueryClientProvider client={client}><CreateKeyWizard open onOpenChange={onOpenChange} orgId={3} sites={sites} initialPurpose={initialPurpose} /></QueryClientProvider>)
  return { dialog: screen.getByRole('dialog', { name: 'Create API key' }), onOpenChange }
}

describe('CreateKeyWizard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    post.mockResolvedValue({ data: { id: 7, key: 'pnl_real_secret', expires_at: '2027-02-16T00:00:00Z' }, response: { ok: true, status: 201 } })
  })
  afterEach(cleanup)

  it('pre-fills scopes from the purpose and plain-words labels', () => {
    const { dialog } = renderWizard()
    fireEvent.click(within(dialog).getByRole('radio', { name: /CI or server/ }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Next' }))
    expect(within(dialog).getByText('Who can use this key')).toBeVisible()
    expect(within(dialog).getByRole('checkbox', { name: /Read analytics/ })).toBeChecked()
    expect(within(dialog).getByRole('checkbox', { name: /Export data/ })).toBeChecked()
    expect(within(dialog).getByRole('checkbox', { name: /Manage projects/ })).not.toBeChecked()
    expect(within(dialog).getByText('stats:read')).toBeVisible()
    expect(within(dialog).getByLabelText('Key name')).toHaveValue('CI or server')
  })

  it('defaults to 90 days and sends expires_in_days', async () => {
    const { dialog } = renderWizard()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Next' }))
    expect(within(dialog).getByRole('combobox', { name: 'Expires' })).toHaveTextContent('In 90 days')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create key' }))
    await waitFor(() => expect(post).toHaveBeenCalledWith('/api/private/orgs/3/api-keys', expect.objectContaining({
      body: { name: 'Stats API', scopes: ['stats:read'], site_ids: null, expires_in_days: 90 },
    })))
  })

  it('warns in amber and sends never_expires for No expiry', async () => {
    const { dialog } = renderWizard()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Next' }))
    fireEvent.click(within(dialog).getByRole('combobox', { name: 'Expires' }))
    fireEvent.click(await screen.findByRole('option', { name: 'No expiry' }))
    expect(within(dialog).getByRole('note')).toHaveTextContent(/stays valid until revoked/)
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create key' }))
    await waitFor(() => expect(post).toHaveBeenCalledWith('/api/private/orgs/3/api-keys', expect.objectContaining({
      body: expect.objectContaining({ never_expires: true }),
    })))
  })

  it('sends selected project ids', async () => {
    const { dialog } = renderWizard()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Next' }))
    fireEvent.click(within(dialog).getByRole('radio', { name: 'Only these projects' }))
    fireEvent.click(within(dialog).getByRole('checkbox', { name: 'Docs' }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create key' }))
    await waitFor(() => expect(post).toHaveBeenCalledWith('/api/private/orgs/3/api-keys', expect.objectContaining({
      body: expect.objectContaining({ site_ids: [20] }),
    })))
  })

  it('MCP purpose ends with the real key inside the client config', async () => {
    const { dialog } = renderWizard()
    fireEvent.click(within(dialog).getByRole('radio', { name: /MCP for an AI assistant/ }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Next' }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create key' }))

    expect(await within(dialog).findByDisplayValue('pnl_real_secret')).toBeVisible()
    expect(within(dialog).getByText("You won't see this key again.")).toBeVisible()
    const config = within(dialog).getByText(/Bearer pnl_real_secret/)
    expect(config).toHaveTextContent('mcpServers')
    expect(config.textContent).not.toContain('<key>')
    expect(within(dialog).getByRole('button', { name: 'Copy Claude Desktop configuration' })).toBeVisible()
    fireEvent.click(within(dialog).getByRole('tab', { name: 'Cursor' }))
    expect(within(dialog).getByText(/Bearer pnl_real_secret/)).toBeVisible()
  })

  it('removes the secret from the DOM once finished', async () => {
    const { dialog, onOpenChange } = renderWizard('mcp')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create key' }))
    await within(dialog).findByDisplayValue('pnl_real_secret')
    fireEvent.click(within(dialog).getByRole('button', { name: 'I have copied the key' }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('shows a server error on the access step', async () => {
    post.mockResolvedValue({ data: undefined, error: { detail: 'This key name is already in use' }, response: { ok: false, status: 409 } })
    const { dialog } = renderWizard()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Next' }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create key' }))
    expect(await within(dialog).findByText('This key name is already in use')).toBeVisible()
  })
})
