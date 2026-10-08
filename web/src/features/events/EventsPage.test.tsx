import '@testing-library/jest-dom/vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ComponentProps } from 'react'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))

vi.mock('@/lib/api', () => ({ api: { GET: get } }))

import { EventsPage } from './EventsPage'

function response(data: unknown) {
  return { data, response: new Response(null, { status: 200 }) }
}

function renderPage(overrides: Partial<ComponentProps<typeof EventsPage>> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const props: ComponentProps<typeof EventsPage> = {
    siteId: 7,
    orgId: 3,
    dateRange: { from: '2026-10-01', to: '2026-10-08' },
    filters: [],
    name: '',
    propertyKey: '',
    onNameChange: vi.fn(),
    onPropertyChange: vi.fn(),
    ...overrides,
  }
  render(<QueryClientProvider client={client}><EventsPage {...props} /></QueryClientProvider>)
  return props
}

describe('EventsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    get.mockImplementation((path: string) => {
      if (path.endsWith('/properties')) return Promise.resolve(response([{ key: 'plan', events: 2 }]))
      if (path.includes('/properties/plan/values')) return Promise.resolve(response([{ value: 'pro', events: 2 }]))
      return Promise.resolve(response({
        data: [{
          ts: '2026-10-08T12:00:00Z', kind: 'event', name: 'signup', page: '/pricing',
          props: { plan: 'pro' }, country: 'US', city: 'Austin', device: 'desktop', browser: 'Chrome', os: 'Windows', source: 'Google',
        }],
        next_cursor: 'next',
      }))
    })
  })

  afterEach(cleanup)

  it('renders raw events without sensitive identity fields', async () => {
    renderPage()

    expect(await screen.findByRole('cell', { name: 'signup' })).toBeVisible()
    expect(screen.getByRole('cell', { name: '/pricing' })).toBeVisible()
    expect(screen.getByText('plan: pro')).toBeVisible()
    expect(screen.getByRole('columnheader', { name: 'Location' })).toBeVisible()
    expect(screen.getByRole('columnheader', { name: 'Browser' })).toBeVisible()
    expect(screen.getByRole('columnheader', { name: 'OS' })).toBeVisible()
    expect(screen.getByRole('columnheader', { name: 'Device' })).toBeVisible()
    expect(screen.getByText('Austin, United States')).toBeVisible()
    expect(screen.queryByText('US')).not.toBeInTheDocument()
    expect(screen.getByText('Windows')).toBeVisible()
    expect(screen.queryByText(/visitor_id|user agent|ip address/i)).not.toBeInTheDocument()
    expect(get).toHaveBeenCalledWith('/api/private/sites/7/events', expect.objectContaining({
      params: expect.objectContaining({
        header: { 'X-Panel-Org': '3' },
        query: expect.objectContaining({ from: '2026-10-01', to: '2026-10-08', limit: 50 }),
      }),
    }))
  })

  it('filters by event name and drills into property values', async () => {
    const props = renderPage({ propertyKey: 'plan' })

    fireEvent.change(screen.getByLabelText('Event name'), { target: { value: 'signup' } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply event name' }))
    expect(props.onNameChange).toHaveBeenCalledWith('signup')

    expect(await screen.findByRole('button', { name: 'Property plan, 2 events' })).toBeVisible()
    expect(await screen.findByText('pro')).toBeVisible()
    expect(screen.getByText('2 events')).toBeVisible()
  })

  it('shows API failures and retries', async () => {
    get.mockRejectedValueOnce(new Error('Could not load events.'))
    renderPage()

    expect(await screen.findByText('Could not load events.')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(get).toHaveBeenCalledTimes(3))
  })
})
