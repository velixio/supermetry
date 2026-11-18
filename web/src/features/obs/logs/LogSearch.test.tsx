import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params }: { children: React.ReactNode; to: string; params: Record<string, string> }) => (
    <a href={Object.entries(params).reduce((href, [key, value]) => href.replace('$' + key, value), to)}>{children}</a>
  ),
}))
const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { ApiProblem } from '@/lib/apiProblem'
import { renderWithQuery } from '@/features/apps/testing'

import type { ObsLog } from './api'
import { formatLogTime } from './LogRow'
import { LogSearch } from './LogSearch'
import { TraceLogs } from './TraceLogs'

afterEach(() => { cleanup(); privateRequest.mockReset() })

const log = (over: Partial<ObsLog> = {}): ObsLog => ({
  ts: '2026-11-18T10:00:00.123Z', severity: 17, severity_text: 'error', service: 'api', environment: 'production', release: '1.0.0',
  body: 'payment failed', truncated: false, attributes: { 'http.route': '/pay' }, ...over,
})

describe('LogSearch', () => {
  it('shows the parser error at its position', async () => {
    privateRequest.mockRejectedValue(new ApiProblem(400, { detail: 'unknown field', position: 9 }))
    renderWithQuery(<LogSearch org="acme" site="desk" siteId={7} timezone="UTC" initialQuery="severity:bogus" />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Search problem at character 10: unknown field')
  })

  it('shows severity, service and site-zone time, and a trace link only with a trace id', async () => {
    privateRequest.mockResolvedValue({ data: [log({ trace_id: 'a'.repeat(32) }), log({ body: 'no trace here', severity_text: 'info' })], next_cursor: null })
    renderWithQuery(<LogSearch org="acme" site="desk" siteId={7} timezone="Asia/Kolkata" />)
    expect(await screen.findByText('payment failed')).toBeInTheDocument()
    expect(screen.getAllByText('api')).toHaveLength(2)
    expect(screen.getByText('error')).toBeInTheDocument()
    expect(screen.getAllByText('2026-11-18 15:30:00.123')).toHaveLength(2)
    expect(screen.getAllByRole('link', { name: 'View trace' })).toHaveLength(1)
    expect(screen.getByRole('link', { name: 'View trace' })).toHaveAttribute('href', '/app/acme/desk/traces/' + 'a'.repeat(32))
  })

  it('loads more with the next cursor', async () => {
    privateRequest
      .mockResolvedValueOnce({ data: [log({ body: 'first page' })], next_cursor: 'cur1' })
      .mockResolvedValueOnce({ data: [log({ body: 'second page' })], next_cursor: null })
    renderWithQuery(<LogSearch org="acme" site="desk" siteId={7} timezone="UTC" />)
    await screen.findByText('first page')
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }))
    expect(await screen.findByText('second page')).toBeInTheDocument()
    expect(String(privateRequest.mock.calls[1][1])).toContain('cursor=cur1')
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument())
  })

  it('shows the setup hint when there are no logs', async () => {
    privateRequest.mockResolvedValue({ data: [], next_cursor: null })
    renderWithQuery(<LogSearch org="acme" site="desk" siteId={7} timezone="UTC" />)
    expect(await screen.findByText(/No logs yet\. Point your OpenTelemetry exporter/)).toBeInTheDocument()
  })

  it('expands a row to show its attributes', async () => {
    privateRequest.mockResolvedValue({ data: [log()], next_cursor: null })
    renderWithQuery(<LogSearch org="acme" site="desk" siteId={7} timezone="UTC" />)
    fireEvent.click(await screen.findByRole('button', { name: 'Show details' }))
    expect(screen.getByText('http.route')).toBeInTheDocument()
    expect(screen.getByText('/pay')).toBeInTheDocument()
  })
})

describe('TraceLogs', () => {
  it('lists the trace logs in the order given (oldest first) without trace links', async () => {
    privateRequest.mockResolvedValue({ data: [log({ body: 'one', trace_id: 'a'.repeat(32) }), log({ body: 'two', trace_id: 'a'.repeat(32) }), log({ body: 'three', trace_id: 'a'.repeat(32) })] })
    renderWithQuery(<TraceLogs org="acme" site="desk" siteId={7} traceId={'a'.repeat(32)} timezone="UTC" around="2026-11-18T10:00:00Z" />)
    const rows = await screen.findAllByRole('listitem')
    expect(rows.map((row) => row.textContent)).toEqual([expect.stringContaining('one'), expect.stringContaining('two'), expect.stringContaining('three')])
    expect(screen.queryByRole('link', { name: 'View trace' })).not.toBeInTheDocument()
    expect(String(privateRequest.mock.calls[0][1])).toContain(`/traces/${'a'.repeat(32)}/logs?`)
  })
})

describe('formatLogTime', () => {
  it('uses the site time zone', () => {
    expect(formatLogTime('2026-11-18T23:30:00.005Z', 'Asia/Kolkata')).toBe('2026-11-19 05:00:00.005')
  })
})
