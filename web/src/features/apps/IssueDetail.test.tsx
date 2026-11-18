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
  assignee_user_id: null, assignee_name: '', priority: 'medium', snooze_until: null, snooze_events: null, snooze_users: null, archived_until_escalating: false, github_url: null,
  series: [{ day: '2026-11-14', events: 5, installs: 3 }],
  breakdowns: {
    release: [{ value: '1.2.0', events: 30, users: 7 }, { value: '1.1.0', events: 12, users: 4 }],
    platform: [{ value: 'windows', events: 42, users: 9 }],
    'tag:channel': [{ value: 'beta', events: 10, users: 2 }],
  },
  latest_events: [{ ts: '2026-11-14T10:00:00Z', release: '1.2.0', platform: 'windows', os_version: '11', arch: 'x64', device_class: 'desktop', runtime: '', exit_reason: '', signal: 'SIGSEGV', exit_code: null, frames: 'render::paint\nrender::flush', tags: {} }],
})

const activityRows = [
  { id: 9, at: '2026-11-14T10:00:00Z', kind: 'status', actor_user_id: 3, actor_name: 'Ada', data: { from: 'open', to: 'resolved', resolved_next: true } },
  { id: 8, at: '2026-11-14T09:00:00Z', kind: 'assigned', actor_user_id: 3, actor_name: 'Ada', data: { assignee: 4 } },
  { id: 7, at: '2026-11-14T08:50:00Z', kind: 'unassigned', actor_user_id: 3, actor_name: 'Ada', data: { from: 4 } },
  { id: 6, at: '2026-11-14T08:40:00Z', kind: 'priority', actor_user_id: 3, actor_name: 'Ada', data: { from: 'medium', to: 'high' } },
  { id: 5, at: '2026-11-14T08:30:00Z', kind: 'snoozed', actor_user_id: 3, actor_name: 'Ada', data: { until: '2026-11-15T08:30:00Z', users: 10 } },
  { id: 4, at: '2026-11-14T08:20:00Z', kind: 'archived', actor_user_id: 3, actor_name: 'Ada', data: { archived: true } },
  { id: 3, at: '2026-11-14T08:10:00Z', kind: 'commented', actor_user_id: 4, actor_name: 'Grace', data: { comment_id: 1 } },
  { id: 2, at: '2026-11-14T08:00:00Z', kind: 'regressed', actor_user_id: null, actor_name: '', data: { release: '1.2.0' } },
  { id: 1, at: '2026-11-01T10:00:00Z', kind: 'first_seen', actor_user_id: null, actor_name: '', data: { release: '1.0.0' } },
]
const feedbackRows = [{ id: 1, at: '2026-11-14T09:00:00Z', issue_id: 11, event_id: 'e1', name: null, has_contact: true, message: 'It froze when I saved', url: '/save' }]
const comments = [{ id: 1, issue_id: 11, author_user_id: 4, author_name: 'Grace', body: 'Looks like the renderer', created_at: '2026-11-14T08:10:00Z', edited_at: null }]

function mockApi(patch: () => Promise<unknown>) {
  privateRequest.mockImplementation((method: string, path: string) => {
    if (method === 'PATCH' || method === 'POST' || method === 'DELETE') return patch()
    if (path.includes('/releases')) return Promise.resolve({ releases: [] })
    if (path.includes('/assignees')) return Promise.resolve({ members: [{ user_id: 3, name: 'Ada' }, { user_id: 4, name: 'Grace' }] })
    if (path.includes('/feedback')) return Promise.resolve({ feedback: feedbackRows })
    if (path.includes('/comments')) return Promise.resolve({ comments })
    if (path.includes('/activity')) return Promise.resolve({ activity: activityRows })
    return Promise.resolve(detail())
  })
}
const props = { org: 'acme', site: 'desk', siteId: 7, issueId: 11, timezone: 'UTC', search: { period: '30d' as const } }

describe('IssueDetail', () => {
  beforeEach(() => vi.clearAllMocks())
  afterEach(cleanup)

  it('shows the introducing release and suspect commits, and nothing when empty', async () => {
    mockApi(() => Promise.resolve({}))
    const base = detail()
    privateRequest.mockImplementation((method: string, path: string) => {
      if (method !== 'GET') return Promise.resolve({})
      if (path.includes('/releases')) return Promise.resolve({ releases: [] })
      if (path.includes('/assignees')) return Promise.resolve({ members: [] })
      if (path.includes('/feedback')) return Promise.resolve({ feedback: [] })
      if (path.includes('/comments')) return Promise.resolve({ comments: [] })
      if (path.includes('/activity')) return Promise.resolve({ activity: [] })
      return Promise.resolve({
        ...base,
        introduced_in: { release: '1.1.0', first_seen: '2026-11-15T09:00:00Z', deploy_at: '2026-11-15T10:00:00Z' },
        suspect_commits: [{ sha: 'abc1234def', repository: 'acme/app', message: 'Fix pay\n\nlong body', author_name: 'Dev One', at: null, path: 'src/pay.ts', score: 20 }],
      })
    })
    renderWithQuery(<IssueDetail {...props} canEdit />)
    expect(await screen.findByTestId('introduced-in')).toHaveTextContent('Introduced in 1.1.0 · deployed')
    const list = screen.getByRole('list', { name: 'Suspect commits' })
    expect(list).toHaveTextContent('abc1234')
    expect(list).not.toHaveTextContent('abc1234d')
    expect(list).toHaveTextContent('Fix pay')
    expect(list).not.toHaveTextContent('long body')
    expect(list).toHaveTextContent('Dev One')
    expect(list).toHaveTextContent('src/pay.ts')
  })

  it('shows no introduced-in line or suspect card when there is nothing to show', async () => {
    mockApi(() => Promise.resolve({}))
    renderWithQuery(<IssueDetail {...props} canEdit />)
    await screen.findByRole('heading', { name: 'Segfault in renderer' })
    expect(screen.queryByTestId('introduced-in')).not.toBeInTheDocument()
    expect(screen.queryByRole('list', { name: 'Suspect commits' })).not.toBeInTheDocument()
  })

  it('lists the user feedback of the issue', async () => {
    mockApi(() => Promise.resolve({}))
    renderWithQuery(<IssueDetail {...props} canEdit />)
    expect(await screen.findByText('It froze when I saved')).toBeInTheDocument()
    const list = screen.getByRole('list', { name: 'User feedback' })
    expect(list).toHaveTextContent('It froze when I saved')
    expect(list).toHaveTextContent('on /save')
    expect(list).toHaveTextContent('left a contact address')
    expect(list).not.toHaveTextContent('@')
  })

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

  it('assigns the issue to a member and changes the priority', async () => {
    mockApi(() => Promise.resolve({}))
    renderWithQuery(<IssueDetail {...props} canEdit />)
    await screen.findByRole('heading', { name: 'Segfault in renderer' })
    await screen.findByRole('option', { name: 'Grace' })
    fireEvent.change(screen.getByLabelText('Assignee'), { target: { value: '4' } })
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('PATCH', '/api/private/sites/7/app/issues/11', { body: { assignee: 4 } }))
    fireEvent.change(screen.getByLabelText('Priority'), { target: { value: 'high' } })
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('PATCH', '/api/private/sites/7/app/issues/11', { body: { priority: 'high' } }))
  })

  it('archives and snoozes from the status controls', async () => {
    mockApi(() => Promise.resolve({}))
    renderWithQuery(<IssueDetail {...props} canEdit />)
    await screen.findByRole('heading', { name: 'Segfault in renderer' })
    fireEvent.click(screen.getByRole('button', { name: 'Archive until escalating' }))
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('PATCH', expect.any(String), { body: { archive: true } }))
    fireEvent.change(screen.getByLabelText('Snooze'), { target: { value: 'u10' } })
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('PATCH', expect.any(String), { body: { snooze: { users: 10 } } }))
  })

  it('posts a comment and lists existing ones', async () => {
    mockApi(() => Promise.resolve({}))
    renderWithQuery(<IssueDetail {...props} canEdit />)
    expect(await screen.findByText('Looks like the renderer')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Comment' })).toBeDisabled()
    fireEvent.change(screen.getByLabelText('Add a comment'), { target: { value: '  Reproduced on 1.2.0  ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Comment' }))
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('POST', '/api/private/sites/7/app/issues/11/comments', { body: { body: 'Reproduced on 1.2.0' } }))
    await waitFor(() => expect(screen.getByLabelText('Add a comment')).toHaveValue(''))
  })

  it('renders every kind of activity on the timeline', async () => {
    mockApi(() => Promise.resolve({}))
    renderWithQuery(<IssueDetail {...props} canEdit />)
    await screen.findByText(/first seen in 1\.0\.0/)
    const timeline = screen.getByRole('list', { name: 'Activity timeline' })
    const text = timeline.textContent ?? ''
    for (const expected of [
      'changed the status from open to resolved in the next release', 'assigned the issue to Grace', 'removed the assignee',
      'changed the priority from medium to high', 'snoozed the issue until 2026-11-15 08:30 or for 10 more users',
      'archived the issue until it escalates', 'commented', 'regressed in 1.2.0', 'first seen in 1.0.0',
    ]) expect(text).toContain(expected)
    expect(text).toContain('System regressed')
  })

  it('shows the workflow read-only to viewers', async () => {
    mockApi(() => Promise.resolve({}))
    renderWithQuery(<IssueDetail {...props} canEdit={false} />)
    await screen.findByRole('heading', { name: 'Segfault in renderer' })
    expect(screen.getByLabelText('Assignee')).toBeDisabled()
    expect(screen.getByLabelText('Priority')).toBeDisabled()
    expect(screen.queryByLabelText('Snooze')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Add a comment')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Archive until escalating' })).not.toBeInTheDocument()
  })

  it('renders the GitHub link only for https github.com URLs', async () => {
    privateRequest.mockImplementation((method: string, path: string) => {
      if (method !== 'GET') return Promise.resolve({})
      if (path.includes('/assignees')) return Promise.resolve({ members: [] })
      if (path.includes('/comments')) return Promise.resolve({ comments: [] })
      if (path.includes('/activity')) return Promise.resolve({ activity: [] })
      return Promise.resolve({ ...detail(), github_url: 'javascript:alert(1)' })
    })
    renderWithQuery(<IssueDetail {...props} canEdit />)
    await screen.findByRole('heading', { name: 'Segfault in renderer' })
    expect(screen.queryByRole('link', { name: 'GitHub issue' })).not.toBeInTheDocument()
  })
})
