import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiProblem } from '@/lib/apiProblem'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))
vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params }: { children: React.ReactNode; to: string; params: Record<string, string> }) => <a href={Object.entries(params).reduce((href, [key, value]) => href.replace('$' + key, value), to)}>{children}</a>,
}))

import { IssuesTable } from './IssuesTable'
import { parseAppIssuesSearch, type AppIssuesSearch } from './search'
import { issueRow, renderWithQuery } from './testing'

function Harness({ initial = parseAppIssuesSearch({}), canEdit = true }: { initial?: AppIssuesSearch; canEdit?: boolean }) {
  const [search, setSearch] = useState(initial)
  return <IssuesTable org="acme" site="desk" siteId={7} timezone="UTC" search={search} onSearch={setSearch} canEdit={canEdit} />
}

const views = [
  { id: 1, name: 'Fatal crashes', query: 'level:fatal', shared: true, mine: false, owner_name: 'Ann', created_at: '2026-11-01T10:00:00Z' },
  { id: 2, name: 'Mine', query: 'assigned:me', shared: false, mine: true, owner_name: 'Me', created_at: '2026-11-01T10:00:00Z' },
]

describe('issue search, saved views and bulk actions', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    privateRequest.mockImplementation((method: string, path: string) => {
      if (path.includes('/issues/bulk')) return Promise.resolve({ updated: [11, 12], skipped: [] })
      if (path.includes('/views')) return method === 'GET' ? Promise.resolve({ views }) : Promise.resolve(views[1])
      if (path.includes('/assignees')) return Promise.resolve({ members: [{ user_id: 5, name: 'Ann' }] })
      if (path.includes('/issues')) return Promise.resolve({ issues: [issueRow(), issueRow({ id: 12, title: 'Timeout in sync' })], total: 120, next_offset: 50 })
      return Promise.resolve({ releases: [] })
    })
  })
  afterEach(cleanup)

  const issueCalls = () => privateRequest.mock.calls.filter(([method, path]) => method === 'GET' && String(path).includes('/issues?')).map(([, path]) => String(path))
  const bulkCalls = () => privateRequest.mock.calls.filter(([, path]) => String(path).includes('/issues/bulk'))

  it('sends the typed query and keeps it in the request', async () => {
    renderWithQuery(<Harness />)
    await screen.findByRole('link', { name: 'Segfault in renderer' })
    fireEvent.change(screen.getByLabelText('Search query'), { target: { value: 'is:unresolved !level:info' } })
    fireEvent.click(screen.getByRole('button', { name: 'Search' }))
    await waitFor(() => expect(issueCalls().some((path) => new URLSearchParams(path.split('?')[1]).get('query') === 'is:unresolved !level:info')).toBe(true))
  })

  it('shows the syntax error and marks its position', async () => {
    privateRequest.mockImplementation((_method: string, path: string) => {
      if (path.includes('/issues?')) return Promise.reject(new ApiProblem(400, { detail: 'unknown search key "bogus"', position: 8 }))
      return Promise.resolve({ views: [], members: [], releases: [] })
    })
    renderWithQuery(<Harness initial={parseAppIssuesSearch({ query: 'timeout bogus:1' })} />)
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('unknown search key "bogus"')
    expect(alert.querySelector('mark')).toHaveTextContent('b')
    expect(alert.querySelector('mark')?.parentElement?.textContent).toBe('timeout bogus:1')
  })

  it('applies a saved view and saves the current query as a new one', async () => {
    renderWithQuery(<Harness />)
    await screen.findByRole('link', { name: 'Segfault in renderer' })
    await screen.findByRole('option', { name: 'Fatal crashes (shared)' })
    fireEvent.change(screen.getByLabelText('Saved views'), { target: { value: '1' } })
    await waitFor(() => expect(issueCalls().some((path) => path.includes('query=level%3Afatal'))).toBe(true))
    fireEvent.click(screen.getByRole('button', { name: 'Save view' }))
    fireEvent.change(screen.getByLabelText('View name'), { target: { value: 'Triage' } })
    fireEvent.click(screen.getByLabelText('Share with the project'))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('POST', '/api/private/sites/7/app/views', { body: { name: 'Triage', query: 'level:fatal', shared: true } }))
  })

  it('bulk-resolves the checked rows by id', async () => {
    renderWithQuery(<Harness />)
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Segfault in renderer' }))
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Timeout in sync' }))
    expect(screen.getByText('2 selected')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Resolve' }))
    await waitFor(() => expect(bulkCalls()).toHaveLength(1))
    expect(bulkCalls()[0]).toEqual(['POST', '/api/private/sites/7/issues/bulk'.replace('/issues', '/app/issues'), { body: { ids: [11, 12], change: { status: 'resolved' } } }])
    expect(await screen.findByText('Updated 2.')).toBeInTheDocument()
    expect(screen.queryByRole('toolbar', { name: 'Bulk actions' })).not.toBeInTheDocument()
  })

  it('offers to act on every matching issue and sends the search, not ids', async () => {
    renderWithQuery(<Harness initial={parseAppIssuesSearch({ query: 'level:fatal', status: 'all' })} />)
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select all issues on this page' }))
    fireEvent.click(screen.getByRole('button', { name: 'Select all 120 matching issues' }))
    expect(screen.getByText('All 120 matching issues')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Set priority of selected'), { target: { value: 'high' } })
    await waitFor(() => expect(bulkCalls()).toHaveLength(1))
    const [, path, options] = bulkCalls()[0]
    expect(options).toEqual({ body: { query: 'level:fatal', change: { priority: 'high' } } })
    expect(new URLSearchParams(String(path).split('?')[1]).get('status')).toBeNull()
  })

  it('shows no checkboxes or save button to viewers', async () => {
    renderWithQuery(<Harness canEdit={false} />)
    await screen.findByRole('link', { name: 'Segfault in renderer' })
    expect(screen.queryAllByRole('checkbox')).toHaveLength(0)
    expect(screen.queryByRole('button', { name: 'Save view' })).not.toBeInTheDocument()
    expect(screen.getByLabelText('Search query')).toBeInTheDocument()
  })

  it('lists the syntax in the help panel', async () => {
    renderWithQuery(<Harness />)
    fireEvent.click(await screen.findByRole('button', { name: 'Syntax help' }))
    expect(screen.getByLabelText('Search syntax')).toHaveTextContent('first-seen:-24h')
  })
})
