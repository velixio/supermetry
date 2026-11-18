import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))
vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params }: { children: React.ReactNode; to: string; params: Record<string, string> }) => <a href={Object.entries(params).reduce((href, [key, value]) => href.replace('$' + key, value), to)}>{children}</a>,
}))

import { IssuesTable } from './IssuesTable'
import { parseAppIssuesSearch, type AppIssuesSearch } from './search'
import { issueRow, renderWithQuery } from './testing'

function Harness({ initial = parseAppIssuesSearch({}) }: { initial?: AppIssuesSearch }) {
  const [search, setSearch] = useState(initial)
  return <IssuesTable org="acme" site="desk" siteId={7} timezone="UTC" search={search} onSearch={setSearch} />
}

describe('IssuesTable', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    privateRequest.mockImplementation((_method: string, path: string) => {
      if (path.includes('/issues')) return Promise.resolve({ issues: [issueRow()], total: 120, next_offset: 50 })
      return Promise.resolve({ releases: [] })
    })
  })
  afterEach(cleanup)

  const issueCalls = () => privateRequest.mock.calls.map(([, path]) => String(path)).filter((path) => path.includes('/issues'))

  it('requests open issues first and renders the row details', async () => {
    renderWithQuery(<Harness />)
    expect(await screen.findByRole('link', { name: 'Segfault in renderer' })).toHaveAttribute('href', '/app/acme/desk/issues/11')
    expect(issueCalls()[0]).toContain('status=open')
    expect(screen.getByText('SIGSEGV')).toBeInTheDocument()
    expect(screen.getByText('1.0.0 → 1.2.0')).toBeInTheDocument()
    expect(screen.getByText('42')).toBeInTheDocument()
  })

  it('changes the request status when a tab is chosen', async () => {
    renderWithQuery(<Harness />)
    await screen.findByRole('link', { name: 'Segfault in renderer' })
    fireEvent.mouseDown(screen.getByRole('tab', { name: 'Regressed' }))
    await waitFor(() => expect(issueCalls().some((path) => path.includes('status=regressed'))).toBe(true))
    fireEvent.mouseDown(screen.getByRole('tab', { name: 'All' }))
    await waitFor(() => expect(issueCalls().length).toBeGreaterThan(2))
    expect(issueCalls().at(-1)).not.toContain('status=')
  })

  it('pages with the offset the API returns', async () => {
    renderWithQuery(<Harness />)
    await screen.findByRole('link', { name: 'Segfault in renderer' })
    fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
    await waitFor(() => expect(issueCalls().some((path) => path.includes('offset=50'))).toBe(true))
  })

  it('filters by Electron process through the search query', async () => {
    renderWithQuery(<Harness initial={parseAppIssuesSearch({ process: 'renderer', query: 'level:fatal' })} />)
    await screen.findByRole('link', { name: 'Segfault in renderer' })
    expect(new URLSearchParams(issueCalls()[0].split('?')[1]).get('query')).toBe('level:fatal process:renderer')
    expect(screen.getByRole('combobox', { name: 'Process' })).toBeInTheDocument()
  })
})
