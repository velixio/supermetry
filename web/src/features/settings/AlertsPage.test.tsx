import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { del, get, patch, post, useGoals } = vi.hoisted(() => ({
  del: vi.fn(),
  get: vi.fn(),
  patch: vi.fn(),
  post: vi.fn(),
  useGoals: vi.fn(),
}))

vi.mock('@/lib/api', () => ({ api: { DELETE: del, GET: get, PATCH: patch, POST: post } }))
vi.mock('@/features/goals/api', () => ({ useGoals }))
vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params }: { children: React.ReactNode; to: string; params: Record<string, string> }) => <a href={Object.entries(params).reduce((href, [key, value]) => href.replace('$' + key, value), to)}>{children}</a>,
}))

import { AlertsPage } from './AlertsPage'

function renderPage(orgSlug?: string) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <AlertsPage orgId={3} orgSlug={orgSlug} siteId={7} />
    </QueryClientProvider>,
  )
}

describe('AlertsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useGoals.mockReturnValue({
      data: [{ id: 11, site_id: 7, kind: 'page', match: 'exact', value: '/thanks', name: 'Signup complete' }],
      isPending: false,
    })
    get.mockImplementation((path: string) => Promise.resolve({
      data: path.endsWith('/channels')
        ? { data: [{ id: 8, org_id: 3, kind: 'email', name: 'Operations', enabled: true, config: { recipients: ['ops@example.com'] }, created_at: '2026-10-08T00:00:00Z' }] }
        : { data: [] },
      response: { ok: true, status: 200 },
    }))
    post.mockResolvedValue({ data: {}, response: { ok: true, status: 201 } })
    patch.mockResolvedValue({ data: {}, response: { ok: true, status: 200 } })
    del.mockResolvedValue({ data: {}, response: { ok: true, status: 204 } })
  })

  afterEach(cleanup)

  it('creates an enabled goal alert for selected notification channels', async () => {
    renderPage()
    await screen.findByText('No alerts yet.')
    fireEvent.click(screen.getByRole('button', { name: 'Create alert' }))
    fireEvent.change(screen.getByLabelText('Alert type'), { target: { value: 'goal_reached' } })
    fireEvent.change(screen.getByLabelText('Goal'), { target: { value: '11' } })
    fireEvent.change(screen.getByLabelText('Threshold'), { target: { value: '25' } })
    fireEvent.click(screen.getByRole('checkbox', { name: 'Operations' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save alert' }))

    await waitFor(() => expect(post).toHaveBeenCalledWith(
      '/api/private/sites/7/alerts',
      expect.objectContaining({
        body: {
          kind: 'goal_reached',
          threshold: 25,
          goal_id: 11,
          channel_ids: [8],
          enabled: true,
        },
      }),
    ))
  })

  it('labels disabled notification channels without selecting them automatically', async () => {
    get.mockImplementation(async (path: string) => ({
      data: path.endsWith('/channels')
        ? { data: [{ id: 8, name: 'Telegram ops', enabled: false }] }
        : path.endsWith('/alerts') ? { data: [] } : [],
      response: { ok: true, status: 200 },
    }))
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Create alert' }))
    expect(screen.getByText('Telegram ops (disabled)')).toBeVisible()
    expect(screen.getByLabelText('Telegram ops')).not.toBeChecked()
    expect(screen.getByLabelText('Telegram ops')).toBeDisabled()
  })

  it('explains each alert threshold in its actual evaluation window', async () => {
    renderPage()
    await screen.findByText('No alerts yet.')
    fireEvent.click(screen.getByRole('button', { name: 'Create alert' }))

    expect(screen.getByText('Active visitors in this process during the last 60 minutes.')).toBeVisible()
    fireEvent.change(screen.getByLabelText('Alert type'), { target: { value: 'traffic_drop' } })
    expect(screen.getByText('Percentage of visitors in the same 24-hour window seven days earlier.')).toBeVisible()
    fireEvent.change(screen.getByLabelText('Alert type'), { target: { value: 'goal_reached' } })
    expect(screen.getByText('Conversions in the current local day.')).toBeVisible()
  })


  it('keeps the three web kinds for a web site', async () => {
    renderPage()
    await screen.findByText('No alerts yet.')
    fireEvent.click(screen.getByRole('button', { name: 'Create alert' }))
    const options = Array.from(screen.getByLabelText('Alert type').querySelectorAll('option')).map((option) => option.value)
    expect(options).toEqual(['traffic_spike', 'traffic_drop', 'goal_reached'])
  })



  it('links to creating a channel when the alert dialog has none', async () => {
    get.mockImplementation(async (path: string) => ({ data: path.endsWith('/alerts') || path.endsWith('/channels') ? { data: [] } : [], response: { ok: true, status: 200 } }))
    renderPage('acme')
    await screen.findByText('No alerts yet.')
    fireEvent.click(screen.getByRole('button', { name: 'Create alert' }))
    expect(await screen.findByRole('link', { name: 'Create a channel' })).toHaveAttribute('href', '/app/acme/settings/notifications')
  })
})
