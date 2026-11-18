import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { AppRulesSection } from './AppRulesSection'

function renderSection(canEdit = true) {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <AppRulesSection orgId={3} siteId={7} canEdit={canEdit} />
    </QueryClientProvider>,
  )
}

const stored = {
  ownership: [{ matcher: 'path', value: 'src/billing/*', owner_user_id: 5 }],
  grouping: [{ matcher: 'error_type', value: 'Timeout*', fingerprint: 'timeouts' }],
}

describe('AppRulesSection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    privateRequest.mockImplementation((method: string, path: string) => {
      if (path.endsWith('/assignees')) return Promise.resolve({ members: [{ user_id: 5, name: 'Ana' }, { user_id: 6, name: 'Bo' }] })
      return Promise.resolve(method === 'PUT' ? stored : stored)
    })
  })
  afterEach(cleanup)

  it('shows stored rules, edits, adds and saves both lists in order', async () => {
    renderSection()
    const own = await screen.findByRole('group', { name: 'Ownership rule 1' })
    await waitFor(() => expect(within(own).getByLabelText('Pattern')).toHaveValue('src/billing/*'))
    expect(within(own).getByLabelText('Owner')).toHaveValue('5')
    fireEvent.change(within(own).getByLabelText('Owner'), { target: { value: '6' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add ownership rule' }))
    const second = screen.getByRole('group', { name: 'Ownership rule 2' })
    fireEvent.change(within(second).getByLabelText('Matcher'), { target: { value: 'tag' } })
    fireEvent.change(within(second).getByLabelText('Tag key'), { target: { value: 'team' } })
    fireEvent.change(within(second).getByLabelText('Pattern'), { target: { value: 'web' } })
    fireEvent.change(within(second).getByLabelText('Owner'), { target: { value: '5' } })
    fireEvent.click(within(second).getByRole('button', { name: 'Move ownership rule up' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save rules' }))
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('PUT', '/api/private/sites/7/app/rules', {
      orgId: 3,
      body: {
        ownership: [
          { matcher: 'tag:team', value: 'web', owner_user_id: 5 },
          { matcher: 'path', value: 'src/billing/*', owner_user_id: 6 },
        ],
        grouping: stored.grouping,
      },
    }))
  })

  it('removes a grouping rule', async () => {
    renderSection()
    await screen.findByRole('group', { name: 'Grouping rule 1' })
    fireEvent.click(screen.getByRole('button', { name: 'Remove grouping rule' }))
    expect(screen.queryByRole('group', { name: 'Grouping rule 1' })).not.toBeInTheDocument()
  })

  it('is read only for viewers', async () => {
    renderSection(false)
    const own = await screen.findByRole('group', { name: 'Ownership rule 1' })
    expect(within(own).getByLabelText('Pattern')).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Save rules' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add ownership rule' })).not.toBeInTheDocument()
  })
})
