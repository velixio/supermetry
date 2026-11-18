import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { renderWithQuery } from '@/features/apps/testing'

import { IssueWebhooks } from './IssueWebhooks'

const channels = { data: [{ id: 5, name: 'Ops hook', kind: 'webhook' }, { id: 6, name: 'Team chat', kind: 'slack' }] }

function mock() {
  privateRequest.mockImplementation((method: string, path: string, options?: { body?: unknown }) => {
    if (path.endsWith('/channels')) return Promise.resolve(channels)
    if (method === 'PUT') return Promise.resolve(options?.body)
    return Promise.resolve({ channel_ids: [5], kinds: ['created'] })
  })
}

describe('IssueWebhooks', () => {
  beforeEach(() => vi.clearAllMocks())
  afterEach(cleanup)

  it('loads the setting and saves the chosen channels and events', async () => {
    mock()
    renderWithQuery(<IssueWebhooks orgId={1} siteId={7} canEdit />)
    const hook = await screen.findByRole('checkbox', { name: /Ops hook/ })
    await waitFor(() => expect(hook).toBeChecked())
    expect(screen.getByRole('checkbox', { name: 'New issue' })).toBeChecked()
    fireEvent.click(screen.getByRole('checkbox', { name: /Team chat/ }))
    fireEvent.click(screen.getByRole('checkbox', { name: 'Resolved' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save webhooks' }))
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('PUT', '/api/private/sites/7/app/issue-webhooks',
      { orgId: 1, body: { channel_ids: [5, 6], kinds: ['created', 'resolved'] } }))
    expect(await screen.findByRole('status')).toHaveTextContent('Saved.')
  })

  it('is read-only for viewers', async () => {
    mock()
    renderWithQuery(<IssueWebhooks orgId={1} siteId={7} canEdit={false} />)
    expect(await screen.findByRole('checkbox', { name: /Ops hook/ })).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Save webhooks' })).not.toBeInTheDocument()
  })
})
