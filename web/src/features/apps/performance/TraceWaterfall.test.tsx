import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params }: { children: React.ReactNode; to: string; params: Record<string, string> }) => (
    <a href={Object.entries(params).reduce((href, [key, value]) => href.replace('$' + key, value), to)}>{children}</a>
  ),
}))
const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { barGeometry, TraceView, TraceWaterfall } from './TraceWaterfall'
import { trace3 } from './performance.fixtures'
import { renderWithQuery } from '../testing'

afterEach(cleanup)

describe('TraceWaterfall', () => {
  it('places bars at offsets proportional to the span start (0%, 10%, 40%)', () => {
    render(<TraceWaterfall org="acme" site="desk" trace={trace3()} />)
    const bars = screen.getAllByTestId('span-bar')
    expect(bars.map((bar) => bar.style.left)).toEqual(['0%', '10%', '40%'])
    expect(bars.map((bar) => bar.style.width)).toEqual(['100%', '20%', '50%'])
  })

  it('marks failing spans and links the errors of the trace to their issue', () => {
    render(<TraceWaterfall org="acme" site="desk" trace={trace3()} />)
    const items = within(screen.getByRole('list', { name: 'Spans' })).getAllByRole('listitem')
    expect(items.map((item) => item.getAttribute('data-failed'))).toEqual(['false', 'false', 'true'])
    expect(within(items[2]).getByText('error')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'TypeError: x is undefined' })).toHaveAttribute('href', '/app/acme/desk/issues/7')
  })

  it('marks a span that only has a linked error, and warns about a truncated trace', () => {
    const trace = trace3()
    trace.spans[2].status = 'ok'
    delete trace.spans[2].http_status
    trace.truncated = true
    render(<TraceWaterfall org="acme" site="desk" trace={trace} />)
    expect(screen.getAllByRole('listitem')[2]).toHaveAttribute('data-failed', 'true')
    expect(screen.getByRole('status')).toHaveTextContent('more spans than can be shown')
  })

  it('keeps bars inside the track and visible for tiny spans', () => {
    const [segment, , failing] = trace3().spans
    expect(barGeometry({ ...segment, start_offset_ms: 99.9, duration_ms: 0.01 }, 100)).toEqual({ left: 99.9, width: expect.closeTo(0.1, 5) })
    expect(barGeometry({ ...failing, start_offset_ms: 0, duration_ms: 0 }, 100).width).toBe(0.5)
    expect(barGeometry(segment, 0)).toEqual({ left: 0, width: 100 })
  })
})

describe('TraceView', () => {
  it('loads the trace by id and shows its waterfall', async () => {
    privateRequest.mockResolvedValue(trace3())
    renderWithQuery(<TraceView org="acme" site="desk" siteId={7} traceId={'a'.repeat(32)} />)
    expect(await screen.findByRole('list', { name: 'Spans' })).toBeInTheDocument()
    expect(privateRequest).toHaveBeenCalledWith('GET', `/api/private/sites/7/app/traces/${'a'.repeat(32)}`)
    expect(screen.getByText(/3 spans/)).toBeInTheDocument()
  })

  it('explains a trace that is gone', async () => {
    privateRequest.mockRejectedValue(new Error('not found'))
    renderWithQuery(<TraceView org="acme" site="desk" siteId={7} traceId={'c'.repeat(32)} />)
    expect(await screen.findByRole('alert')).toHaveTextContent('14 days')
  })
})
