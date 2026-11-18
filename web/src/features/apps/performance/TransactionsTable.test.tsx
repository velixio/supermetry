import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params }: { children: React.ReactNode; to: string; params: Record<string, string> }) => (
    <a href={Object.entries(params).reduce((href, [key, value]) => href.replace('$' + key, encodeURIComponent(value)), to)}>{children}</a>
  ),
}))

import { TransactionsTable } from './TransactionsTable'
import { txRow } from './performance.fixtures'

afterEach(cleanup)

const rows = [
  txRow({ name: 'GET /fast', p50_ms: 5, p95_ms: 12.34, count: 900, errors: 0, error_rate: 0 }),
  txRow({ name: 'GET /slow', p95_ms: 480.25, count: 10, errors: 4, error_rate: 0.4 }),
  txRow({ name: 'POST /mid', p95_ms: 100, count: 50, errors: 1, error_rate: null }),
]
const names = () => screen.getAllByRole('row').slice(1).map((row) => row.querySelector('a')!.textContent)

describe('TransactionsTable', () => {
  it('sorts by p95 (slowest first) and shows milliseconds with one decimal', () => {
    render(<TransactionsTable org="acme" site="desk" rows={rows} search={{ period: '30d' }} />)
    expect(names()).toEqual(['GET /slow', 'POST /mid', 'GET /fast'])
    const slow = screen.getByText('GET /slow').closest('tr')!
    expect(slow).toHaveTextContent('480.3 ms')
    expect(screen.getByText('GET /fast').closest('tr')).toHaveTextContent('12.3 ms')
    expect(slow).toHaveTextContent('40.0%')
    expect(screen.getByText('POST /mid').closest('tr')).toHaveTextContent('n/a')
  })

  it('re-sorts when a column header is clicked', () => {
    render(<TransactionsTable org="acme" site="desk" rows={rows} search={{ period: '30d' }} />)
    fireEvent.click(screen.getByRole('button', { name: 'Count' }))
    expect(names()).toEqual(['GET /fast', 'POST /mid', 'GET /slow'])
    fireEvent.click(screen.getByRole('button', { name: 'Errors' }))
    expect(names()).toEqual(['GET /slow', 'POST /mid', 'GET /fast'])
  })

  it('links each transaction to its detail page with the name encoded', () => {
    render(<TransactionsTable org="acme" site="desk" rows={rows} search={{ period: '30d' }} />)
    expect(screen.getByRole('link', { name: 'GET /slow' })).toHaveAttribute('href', '/app/acme/desk/performance/GET%20%2Fslow')
  })
})
