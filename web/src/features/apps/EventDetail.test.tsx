import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({ Link: ({ children, to }: { children: React.ReactNode; to: string }) => <a href={to}>{children}</a> }))
const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { EventBodyView, EventDetail } from './EventDetail'
import { renderWithQuery } from './testing'

const body = {
  message: 'checkout failed',
  exceptions: [
    { type: 'TypeError', value: 'x is undefined', mechanism: { type: 'onerror', handled: false }, frames: [
      { filename: 'node_modules/lib/index.js', function: 'dispatch', lineno: 10, in_app: false },
      { filename: 'src/cart.ts', function: 'pay', lineno: 42, colno: 7, in_app: true, context_line: 'total.value()', pre_context: ['const total = cart.total'], post_context: ['return 1'] },
    ] },
    { type: 'RangeError', value: 'cause value', frames: [] },
  ],
  breadcrumbs: [{ timestamp: '2026-11-14T10:00:01Z', type: 'navigation', category: 'navigation', level: 'info', message: 'to /cart' }],
  contexts: { os: { name: 'Windows', version: '11' }, browser: { name: 'Chrome' } },
  request: { method: 'GET', url: '/cart' },
  tags: { channel: 'beta' },
  attachments: [{ name: 'log.txt', type: 'text/plain', size: 12 }],
  sdk: { name: 'sentry.javascript.browser', version: '8.0.0' },
}

afterEach(cleanup)

describe('EventBodyView', () => {
  it('renders the exception chain with in-app frames highlighted, breadcrumbs, contexts and tags', () => {
    render(<EventBodyView body={body} />)
    expect(screen.getByText('TypeError')).toBeInTheDocument()
    expect(screen.getByText('unhandled')).toBeInTheDocument()
    expect(screen.getByText('RangeError')).toBeInTheDocument()
    const frames = within(screen.getByLabelText('Stack frames for TypeError')).getAllByRole('listitem')
    // The crash site comes first.
    expect(frames[0]).toHaveAttribute('data-in-app', 'true')
    expect(frames[0]).toHaveTextContent('src/cart.ts:42:7')
    expect(frames[0]).toHaveTextContent('total.value()')
    expect(frames[1]).toHaveAttribute('data-in-app', 'false')
    expect(within(screen.getByLabelText('Breadcrumbs')).getByText('to /cart')).toBeInTheDocument()
    expect(screen.getByText('Windows')).toBeInTheDocument()
    expect(screen.getByText('Chrome')).toBeInTheDocument()
    expect(screen.getByLabelText('Tags')).toHaveTextContent('channel: beta')
    expect(screen.getByText(/log\.txt/)).toBeInTheDocument()
    expect(screen.getByText(/sentry\.javascript\.browser 8\.0\.0/)).toBeInTheDocument()
  })

  it('warns when source maps are missing for the release and links to the upload guide', () => {
    render(<EventBodyView body={{ ...body, release: '1.4.2', symbolication: 'missing' }} />)
    const banner = screen.getByRole('status', { name: 'Source maps' })
    expect(banner).toHaveTextContent('Source maps missing for release 1.4.2')
    expect(within(banner).getByRole('link', { name: /upload source maps/i })).toHaveAttribute('href', expect.stringContaining('docs/guide/source-maps.md'))
    cleanup()
    render(<EventBodyView body={{ ...body, release: '1.4.2', symbolication: 'partial' }} />)
    expect(screen.getByRole('status', { name: 'Source maps' })).toHaveTextContent('Some frames could not be resolved')
    cleanup()
    render(<EventBodyView body={{ ...body, release: '1.4.2', symbolication: 'ok' }} />)
    expect(screen.queryByRole('status', { name: 'Source maps' })).not.toBeInTheDocument()
    cleanup()
    render(<EventBodyView body={{ ...body, symbolication: 'missing' }} />)
    expect(screen.getByRole('status', { name: 'Source maps' })).toHaveTextContent('Source maps missing for this release')
  })

  it('tolerates missing fields', () => {
    render(<EventBodyView body={{ exceptions: [{ frames: [{}] }], breadcrumbs: 'bad', contexts: { os: null } }} />)
    expect(screen.getByText('Error')).toBeInTheDocument()
    expect(screen.getByText('<unknown>')).toBeInTheDocument()
    cleanup()
    render(<EventBodyView body={null} />)
    expect(screen.getByText('This event has no stored detail.')).toBeInTheDocument()
  })
})

describe('EventDetail', () => {
  it('loads the body from the private API', async () => {
    privateRequest.mockResolvedValue({ event_id: 'abc123', ts: '2026-11-14T10:00:00Z', body })
    renderWithQuery(<EventDetail org="acme" site="web" siteId={7} issueId={3} eventId="abc123" />)
    expect(await screen.findByText('checkout failed')).toBeInTheDocument()
    expect(privateRequest).toHaveBeenCalledWith('GET', '/api/private/sites/7/app/events/abc123')
  })

  it('offers "View trace" only when the event has a trace id', async () => {
    privateRequest.mockResolvedValue({ event_id: 'abc123', ts: '2026-11-14T10:00:00Z', trace_id: 'a'.repeat(32), body })
    const first = renderWithQuery(<EventDetail org="acme" site="web" siteId={7} issueId={3} eventId="abc123" />)
    expect(await screen.findByRole('link', { name: 'View trace' })).toHaveAttribute('href', '/app/$org/$site/traces/$trace')
    first.unmount()
    privateRequest.mockResolvedValue({ event_id: 'def456', ts: '2026-11-14T10:00:00Z', body })
    renderWithQuery(<EventDetail org="acme" site="web" siteId={7} issueId={3} eventId="def456" />)
    expect(await screen.findByText('checkout failed')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'View trace' })).not.toBeInTheDocument()
  })

  it('explains an event whose body has aged out', async () => {
    privateRequest.mockRejectedValue(new Error('event not found'))
    renderWithQuery(<EventDetail org="acme" site="web" siteId={7} issueId={3} eventId="old" />)
    expect(await screen.findByRole('alert')).toHaveTextContent('kept for 30 days')
  })
})
