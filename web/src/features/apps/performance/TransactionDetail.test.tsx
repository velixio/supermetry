import '@testing-library/jest-dom/vitest'

import { cleanup, screen, within } from '@testing-library/react'
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
import { detailData } from './performance.fixtures'
import { TransactionDetail } from './TransactionDetail'

afterEach(cleanup)
beforeEach(() => { privateRequest.mockReset() })

const mount = () => renderWithQuery(<TransactionDetail org="acme" site="desk" siteId={7} name="GET /items" timezone="UTC" search={{ period: '30d' }} onSearch={vi.fn()} />)

describe('TransactionDetail', () => {
  it('shows the latency chart, span breakdown and slow samples linking to their trace', async () => {
    privateRequest.mockResolvedValue(detailData())
    mount()
    expect(await screen.findByRole('img', { name: 'p50 and p95 latency per day' })).toBeInTheDocument()
    const query = privateRequest.mock.calls[0][1] as string
    expect(query).toContain('/api/private/sites/7/app/transactions/detail?')
    expect(new URLSearchParams(query.split('?')[1]).get('name')).toBe('GET /items')
    const groups = within(screen.getByRole('table', { name: 'Span groups' }))
    expect(groups.getByText('select * from users where id = ?')).toBeInTheDocument()
    expect(groups.getByText('1200.5 ms')).toBeInTheDocument()
    const sample = screen.getByRole('link', { name: `${'b'.repeat(12)}…` })
    expect(sample).toHaveAttribute('href', `/app/acme/desk/traces/${'b'.repeat(32)}`)
    expect(sample.closest('tr')).toHaveTextContent('480.3 ms')
    expect(screen.getByLabelText('App start')).toHaveTextContent('cold start: p50 1000.0 ms, p95 3000.0 ms (4 starts)')
  })

  it('says so when there are no samples', async () => {
    privateRequest.mockResolvedValue({ ...detailData(), series: [], span_groups: [], slowest: [], app_start: [] })
    mount()
    expect(await screen.findByText('No samples in this period.')).toBeInTheDocument()
    expect(screen.getByText('No child spans in this period.')).toBeInTheDocument()
  })

  it('shows the API error', async () => {
    privateRequest.mockRejectedValue(new Error('transaction not found'))
    mount()
    expect(await screen.findByRole('alert')).toHaveTextContent('transaction not found')
  })
})
