import '@testing-library/jest-dom/vitest'

import { cleanup, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params }: { children: React.ReactNode; to: string; params: Record<string, string> }) => (
    <a href={Object.entries(params).reduce((href, [key, value]) => href.replace('$' + key, value), to)}>{children}</a>
  ),
}))
const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))
vi.mock('../AppFilters', () => ({ AppFilters: () => null }))

import { renderWithQuery } from '../testing'
import { PerformanceOverview } from './PerformanceOverview'
import { txRow } from './performance.fixtures'

afterEach(cleanup)

const overview = (over = {}) => ({
  transactions: 110, errors: 5, error_rate: 5 / 110, p50_ms: 55, p95_ms: 97.2,
  slowest: [txRow()], most_failing: [txRow({ name: 'POST /pay', error_rate: 0.4 })], app_start: [], ...over,
})
const respond = (data: unknown, regressions = { regressions: [] as unknown[] }) => privateRequest.mockImplementation(async (_m: string, path: string) => {
  if (path.includes('/performance/regressions')) return regressions
  if (path.includes('/performance/network')) return { failures: [{ op: 'http.client', description: 'GET https://api.example.com/v1/items/*', count: 4, errors: 1, error_rate: 0.25, total_ms: 400, avg_ms: 100, p95_ms: 120 }] }
  if (path.includes('/transactions?')) return { transactions: [txRow()] }
  if (path.includes('/releases')) return { releases: [{ release: '1.2.0' }, { release: '1.1.0' }] }
  return data
})
const mount = () => renderWithQuery(<PerformanceOverview org="acme" site="desk" siteId={7} timezone="UTC" search={{ period: '30d' }} onSearch={vi.fn()} />)

beforeEach(() => { privateRequest.mockReset() })

describe('PerformanceOverview', () => {
  it('shows totals, transactions, network failures and the web vitals link', async () => {
    respond(overview({ vitals_link: { site_id: 3, lcp_p75: 2400.4, cls_p75: 0.1 } }))
    mount()
    expect(await screen.findByLabelText('Key figures')).toHaveTextContent('97.2 ms')
    expect(screen.getByRole('table', { name: 'Transactions' })).toHaveTextContent('GET /items')
    expect(await screen.findByRole('table', { name: 'Network failures' })).toHaveTextContent('25.0%')
    expect(screen.getByText('LCP 2400 ms')).toBeInTheDocument()
    expect(screen.getByText('INP n/a')).toBeInTheDocument()
    expect(screen.getByText('CLS 0.100')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open Web Vitals' })).toHaveAttribute('href', '/app/acme/3/vitals')
  })

  it('omits the vitals card without a linked web site', async () => {
    respond(overview())
    mount()
    await screen.findByLabelText('Key figures')
    expect(screen.queryByText('Web Vitals')).not.toBeInTheDocument()
  })

  it('points to setup when there are no transactions', async () => {
    respond(overview({ transactions: 0, slowest: [], most_failing: [] }))
    mount()
    expect(await screen.findByText('No transactions in this period.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Setup instructions' })).toHaveAttribute('href', '/app/acme/desk/settings/installation')
  })

  it('lists regressions between the two newest releases', async () => {
    respond(overview(), { regressions: [{ name: 'GET /items', op: 'http.server', base_p95_ms: 100, target_p95_ms: 130, base_count: 40, target_count: 40, delta: 0.3 }] })
    mount()
    const table = await screen.findByRole('table', { name: 'Regressions' })
    expect(table).toHaveTextContent('+30%')
    expect(table).toHaveTextContent('130.0 ms')
    await waitFor(() => {
      const call = privateRequest.mock.calls.find(([, path]) => (path as string).includes('/performance/regressions'))!
      const query = new URLSearchParams((call[1] as string).split('?')[1])
      expect([query.get('base'), query.get('target')]).toEqual(['1.1.0', '1.2.0'])
    })
  })
})
