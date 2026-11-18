import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { IssueMerge, parseIssueIds } from './IssueMerge'
import { renderWithQuery } from './testing'

const hash = 'ab'.repeat(32)
const merged = [{ issue_id: 5, hash, title: 'Old crash', kind: 'crash', events: 9, last_seen: '2026-11-14T10:00:00Z' }]

describe('parseIssueIds', () => {
  it('accepts comma and space separated positive ids and rejects anything else', () => {
    expect(parseIssueIds('12, 15 16')).toEqual([12, 15, 16])
    expect(parseIssueIds('')).toBeNull()
    expect(parseIssueIds('12,x')).toBeNull()
    expect(parseIssueIds('0')).toBeNull()
    expect(parseIssueIds('-3')).toBeNull()
  })
})

describe('IssueMerge', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    privateRequest.mockImplementation((method: string, path: string) => {
      if (method === 'GET' && path.endsWith('/merged')) return Promise.resolve({ merged })
      return Promise.resolve({})
    })
  })
  afterEach(cleanup)

  it('lists merged issues and unmerges one by its hash', async () => {
    renderWithQuery(<IssueMerge siteId={7} issueId={11} canEdit />)
    expect(await screen.findByText('Old crash')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Unmerge Old crash' }))
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('POST', '/api/private/sites/7/app/issues/11/unmerge', { body: { hashes: [hash] } }))
  })

  it('merges the typed issue ids', async () => {
    renderWithQuery(<IssueMerge siteId={7} issueId={11} canEdit />)
    await screen.findByText('Old crash')
    expect(screen.getByRole('button', { name: 'Merge' })).toBeDisabled()
    fireEvent.change(screen.getByLabelText('Issue ids to merge in'), { target: { value: '12, 15' } })
    fireEvent.click(screen.getByRole('button', { name: 'Merge' }))
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('POST', '/api/private/sites/7/app/issues/11/merge', { body: { source_ids: [12, 15] } }))
  })

  it('shows viewers the list without any control', async () => {
    renderWithQuery(<IssueMerge siteId={7} issueId={11} canEdit={false} />)
    expect(await screen.findByText('Old crash')).toBeInTheDocument()
    expect(screen.queryAllByRole('button').filter((button) => button.getAttribute('data-slot') !== 'info-tip')).toHaveLength(0)
  })
})
