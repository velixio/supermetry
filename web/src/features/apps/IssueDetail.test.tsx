import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))
const { copyText } = vi.hoisted(() => ({ copyText: vi.fn(async () => true) }))
vi.mock('@/lib/clipboard', () => ({ copyText }))
vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to }: { children: React.ReactNode; to: string }) => <a href={to}>{children}</a>,
}))

import { IssueDetail } from './IssueDetail'
import { renderWithQuery } from './testing'

const detail = (status = 'open') => ({
  id: 11, title: 'Segfault in renderer', kind: 'crash', error_type: 'SIGSEGV', status,
  first_seen: '2026-11-01T10:00:00Z', last_seen: '2026-11-14T10:00:00Z', first_release: '1.0.0', last_release: '1.2.0',
  resolved_in_release: null, resolved_next: false, event_count: 42,
  series: [{ day: '2026-11-14', events: 5, installs: 3 }],
  breakdowns: {
    release: [{ value: '1.2.0', events: 30, users: 7 }, { value: '1.1.0', events: 12, users: 4 }],
    platform: [{ value: 'windows', events: 42, users: 9 }],
    'tag:channel': [{ value: 'beta', events: 10, users: 2 }],
  },
  latest_events: [{ ts: '2026-11-14T10:00:00Z', release: '1.2.0', platform: 'windows', os_version: '11', arch: 'x64', device_class: 'desktop', runtime: '', exit_reason: '', signal: 'SIGSEGV', exit_code: null, frames: 'render::paint\nrender::flush', tags: {} }],
})

function mockApi(patch: () => Promise<unknown>) {
  privateRequest.mockImplementation((method: string, path: string) => {
    if (method === 'PATCH') return patch()
    if (path.includes('/releases')) return Promise.resolve({ releases: [] })
    return Promise.resolve(detail())
  })
}
const props = { org: 'acme', site: 'desk', siteId: 7, issueId: 11, timezone: 'UTC', search: { period: '30d' as const } }

describe('IssueDetail', () => {
  beforeEach(() => vi.clearAllMocks())
  afterEach(cleanup)

  it('renders the header, breakdowns and expandable frames with a copy button', async () => {
    mockApi(() => Promise.resolve({}))
    renderWithQuery(<IssueDetail {...props} canEdit />)
    expect(await screen.findByRole('heading', { name: 'Segfault in renderer' })).toBeInTheDocument()
    expect(screen.getByText('crash', { selector: '[data-slot="badge"]' })).toBeInTheDocument()
    expect(screen.getByText('1.1.0')).toBeInTheDocument()
    expect(screen.getByText('channel')).toBeInTheDocument()
    expect(screen.queryByText(/render::paint/)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /Windows|windows/ }))
    expect(screen.getByText(/render::paint/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Copy frames' }))
    expect(copyText).toHaveBeenCalledWith('render::paint\nrender::flush')
  })

  it('updates the status optimistically and rolls back when the PATCH fails', async () => {
    let fail: (error: Error) => void = () => undefined
    mockApi(() => new Promise((_resolve, reject) => { fail = reject }))
    renderWithQuery(<IssueDetail {...props} canEdit />)
    await screen.findByRole('heading', { name: 'Segfault in renderer' })
    fireEvent.click(screen.getByRole('button', { name: 'Resolve' }))
    // Optimistic: reopen is offered while the request is still pending.
    expect(await screen.findByRole('button', { name: 'Reopen' })).toBeInTheDocument()
    expect(privateRequest).toHaveBeenCalledWith('PATCH', '/api/private/sites/7/app/issues/11', { body: { status: 'resolved' } })
    fail(new Error('500 Internal Server Error'))
    expect(await screen.findByRole('button', { name: 'Resolve' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Reopen' })).not.toBeInTheDocument()
    expect(await screen.findByRole('alert')).toHaveTextContent('500 Internal Server Error')
  })

  it('resolves in the next release and ignores', async () => {
    mockApi(() => Promise.resolve({}))
    renderWithQuery(<IssueDetail {...props} canEdit />)
    await screen.findByRole('heading', { name: 'Segfault in renderer' })
    fireEvent.click(screen.getByRole('button', { name: 'Resolve in next release' }))
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('PATCH', expect.any(String), { body: { status: 'resolved', resolved_in_release: 'next' } }))
  })

  it('hides the actions from viewers', async () => {
    mockApi(() => Promise.resolve({}))
    renderWithQuery(<IssueDetail {...props} canEdit={false} />)
    await screen.findByRole('heading', { name: 'Segfault in renderer' })
    for (const name of ['Resolve', 'Resolve in next release', 'Ignore', 'Reopen']) expect(screen.queryByRole('button', { name })).not.toBeInTheDocument()
  })
})
