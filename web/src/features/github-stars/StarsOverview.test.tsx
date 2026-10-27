import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('@tanstack/react-router', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@tanstack/react-router')>()),
  Link: ({ to, params, children, ...rest }: { to: string; params?: Record<string, string>; children: React.ReactNode }) => (
    <a href={to.replace(/\$(\w+)/g, (_m, key: string) => params?.[key] ?? '')} {...rest}>{children}</a>
  ),
}))

const api = vi.hoisted(() => ({
  useStarsRepos: vi.fn(), useStarsSummary: vi.fn(), useStarsSeries: vi.fn(), useAddStarsRepo: vi.fn(),
}))
vi.mock('./githubStarsApi', async (importOriginal) => ({ ...(await importOriginal<typeof import('./githubStarsApi')>()), ...api }))

import { StarsOverview } from './StarsOverview'

const repo = (over: Record<string, unknown> = {}) => ({
  id: 1, org_id: 1, repo: 'velixio/supermetry', public_counter: false, enabled: true, time_zone: 'UTC', has_token: false,
  webhook_url: 'https://stats.example.test/api/github/webhook/abc', public_count_url: 'https://stats.example.test/api/github/stars/velixio/supermetry',
  created_at: '2026-10-01T00:00:00Z', last_webhook_at: null, last_reconcile_at: '2026-10-03T00:00:00Z', last_error: null, history_complete: true, ...over,
})
const summary = (stars: number, net7: number, recorded = stars) => ({ isPending: false, isError: false, data: { stars, forks: 1, watchers: 1, open_issues: 1, updated_at: null, net_24h: 0, net_7d: net7, recorded } })
const series = (first: number, last: number) => ({
  isPending: false, isError: false,
  data: { time_zone: 'UTC', data: [{ bucket: '2026-09-14T00:00:00Z', gained: 0, lost: 0, net: 0, cumulative: first }, { bucket: '2026-10-13T00:00:00Z', gained: 1, lost: 0, net: 1, cumulative: last }] },
})
const mutation = () => ({ mutate: vi.fn(), reset: vi.fn(), isPending: false, error: null })

function ready(repos: unknown[]) {
  api.useStarsRepos.mockReturnValue({ isPending: false, isError: false, data: { data: repos } })
  api.useAddStarsRepo.mockReturnValue(mutation())
}

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('StarsOverview', () => {
  it('shows one card per repository with the full count, change and sparkline, as a single link', () => {
    ready([repo(), repo({ id: 2, repo: 'velixio/docs', last_reconcile_at: null })])
    api.useStarsSummary.mockImplementation((id: number) => (id === 1 ? summary(82412, 1500) : summary(9, -3)))
    api.useStarsSeries.mockImplementation((id: number) => (id === 1 ? series(82100, 82412) : series(12, 9)))
    render(<StarsOverview org="acme" orgId={1} canManage />)

    expect(screen.getByRole('heading', { level: 1, name: 'GitHub stars' })).toBeVisible()
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
    const first = screen.getByRole('link', { name: 'velixio/supermetry, 82,412 stars' })
    expect(first).toHaveAttribute('href', '/app/acme/github-stars/velixio/supermetry')
    expect(within(first).getByText('82,412')).toBeVisible()
    expect(within(first).queryByText(/82\.4K/)).not.toBeInTheDocument()
    expect(within(first).getByText('+1,500 this week')).toBeVisible()
    expect(within(first).getByRole('img', { name: 'Stars over the last 30 days: from 82,100 to 82,412' })).toBeInTheDocument()
    expect(within(first).getByText(/^Synced /)).toBeVisible()
    const second = screen.getByRole('link', { name: 'velixio/docs, 9 stars' })
    expect(within(second).getByText('-3 this week')).toBeVisible()
    expect(within(second).getByText('Never synced')).toBeVisible()
    expect(screen.getAllByRole('link')).toHaveLength(2)
  })

  it('truncates long names with a title and shows disabled and error statuses', () => {
    const long = 'some-organization-with-a-long-name/an-equally-long-repository-name'
    ready([repo({ id: 3, repo: long, enabled: false }), repo({ id: 4, repo: 'a/b', last_error: 'x'.repeat(200) })])
    api.useStarsSummary.mockReturnValue(summary(1, 0))
    api.useStarsSeries.mockReturnValue(series(1, 1))
    render(<StarsOverview org="acme" orgId={1} canManage />)
    const name = screen.getByText(long)
    expect(name).toHaveAttribute('title', long)
    expect(name).toHaveClass('truncate')
    expect(screen.getByText('Disabled')).toBeVisible()
    expect(screen.getByText(`${'x'.repeat(80)}…`)).toBeVisible()
  })

  it('isolates a failing card from the rest of the grid', () => {
    ready([repo(), repo({ id: 2, repo: 'velixio/docs' })])
    api.useStarsSummary.mockImplementation((id: number) => (id === 1 ? { isPending: false, isError: true, error: new Error('boom') } : summary(5, 1)))
    api.useStarsSeries.mockImplementation((id: number) => (id === 1 ? { isPending: false, isError: true, error: new Error('boom') } : series(4, 5)))
    render(<StarsOverview org="acme" orgId={1} canManage />)
    expect(screen.getByText('Numbers could not be loaded')).toBeVisible()
    expect(screen.getByRole('link', { name: 'velixio/supermetry' })).toBeVisible()
    expect(screen.getByRole('link', { name: 'velixio/docs, 5 stars' })).toBeVisible()
  })

  it('shows a skeleton while a card loads', () => {
    ready([repo()])
    api.useStarsSummary.mockReturnValue({ isPending: true })
    api.useStarsSeries.mockReturnValue({ isPending: true })
    render(<StarsOverview org="acme" orgId={1} canManage />)
    expect(screen.getByRole('link', { name: 'velixio/supermetry' })).toBeVisible()
    expect(screen.queryByRole('img')).not.toBeInTheDocument()
  })

  it('invites adding the first repository and opens the dialog', () => {
    ready([])
    render(<StarsOverview org="acme" orgId={1} canManage />)
    expect(screen.getByText('Add your first repository')).toBeVisible()
    expect(screen.queryByText(/SUPERMETRY_GITHUB/)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Add repository' }))
    expect(screen.getByRole('dialog', { name: 'Add repository' })).toBeVisible()
  })

  it('adds a repository and shows the one-time secret on the overview', () => {
    ready([repo()])
    api.useStarsSummary.mockReturnValue(summary(1, 0))
    api.useStarsSeries.mockReturnValue(series(1, 1))
    const add = mutation()
    add.mutate.mockImplementation((_body: unknown, options: { onSuccess: (r: unknown) => void }) => options.onSuccess({
      repo: repo({ id: 2, repo: 'velixio/docs' }), webhook_secret: 'topsecret', webhook_url: 'https://stats.example.test/api/github/webhook/zzzz',
    }))
    api.useAddStarsRepo.mockReturnValue(add)
    render(<StarsOverview org="acme" orgId={1} canManage />)
    fireEvent.click(screen.getByRole('button', { name: 'Add repository' }))
    fireEvent.change(screen.getByLabelText('Repository (owner/name)'), { target: { value: 'velixio/docs' } })
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Add repository' }))
    const panel = screen.getByRole('region', { name: 'Webhook for velixio/docs' })
    expect(within(panel).getByText('topsecret')).toBeVisible()
    fireEvent.click(within(panel).getByRole('button', { name: 'I have saved the secret' }))
    expect(screen.queryByText('topsecret')).not.toBeInTheDocument()
  })

  it('reports a list failure with a retry', () => {
    const refetch = vi.fn()
    api.useStarsRepos.mockReturnValue({ isPending: false, isError: true, error: new Error('boom'), refetch })
    render(<StarsOverview org="acme" orgId={1} canManage />)
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(refetch).toHaveBeenCalled()
  })

  it('shows the import progress on a card only while the history is unfinished', () => {
    ready([repo({ history_complete: false, has_token: true }), repo({ id: 2, repo: 'velixio/docs', history_complete: false, has_token: false }), repo({ id: 3, repo: 'velixio/done' })])
    api.useStarsSummary.mockImplementation((id: number) => (id === 3 ? summary(10, 0) : summary(82418, 5, 31200)))
    api.useStarsSeries.mockReturnValue(series(1, 2))
    render(<StarsOverview org="acme" orgId={1} canManage />)
    const first = screen.getByRole('link', { name: 'velixio/supermetry, 82,418 stars' })
    expect(within(first).getByRole('status')).toHaveTextContent('History import: 31,200 of 82,418 stars (resumes automatically)')
    expect(within(screen.getByRole('link', { name: 'velixio/docs, 82,418 stars' })).getByRole('status')).toHaveTextContent('needs a GitHub token')
    expect(within(screen.getByRole('link', { name: 'velixio/done, 10 stars' })).queryByRole('status')).toBeNull()
  })

  it('is view-only for plain members: no add button, and an empty state that points to an owner or admin', () => {
    ready([repo()])
    api.useStarsSummary.mockReturnValue(summary(5, 1))
    api.useStarsSeries.mockReturnValue(series(1, 5))
    render(<StarsOverview org="acme" orgId={1} canManage={false} />)
    expect(screen.getByRole('link', { name: 'velixio/supermetry, 5 stars' })).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Add repository' })).not.toBeInTheDocument()
    cleanup()
    ready([])
    render(<StarsOverview org="acme" orgId={1} canManage={false} />)
    expect(screen.getByText('No repositories tracked yet')).toBeVisible()
    expect(screen.getByText('Ask an owner or admin of this organization to add a repository.')).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Add repository' })).not.toBeInTheDocument()
  })

  it('invites owners and admins to add the first repository', () => {
    ready([])
    render(<StarsOverview org="acme" orgId={1} canManage />)
    expect(screen.getByText('Add your first repository')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Add repository' })).toBeVisible()
  })
})
