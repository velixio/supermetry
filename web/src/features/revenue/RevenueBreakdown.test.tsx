import '@testing-library/jest-dom/vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { RevenueBreakdown } from './RevenueBreakdown'
import { reportPath } from './revenueReportApi'

const report = {
  currency: 'USD', breakdown: 'source', attribution: 'event', lookback_days: 30,
  orders: 5, revenue: 250, average_order_value: 50, visitors: 100, revenue_per_visitor: 2.5, truncated: false,
  rows: [
    { key: 'newsletter', orders: 3, revenue: 200, average_order_value: 66.67 },
    { key: '', orders: 2, revenue: 50, average_order_value: 25 },
  ],
}
const identified = { ...report, unique_customers: 4, repeat_customers: 1, repeat_rate: 25 }
const customers = { data: [{ profile_id: 1, external_id: 'cust-42', orders: 2, revenue: 120, first_order_at: '2026-03-01T10:00:00Z', last_order_at: '2026-03-05T10:00:00Z' }] }

function reply(status: number, body: unknown) {
  return Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
}

function mount() {
  const client = new QueryClient()
  return render(<QueryClientProvider client={client}>
    <RevenueBreakdown siteId={7} orgId={3} currency="USD" timezone="UTC" from="2026-03-01" to="2026-03-09" />
  </QueryClientProvider>)
}

describe('RevenueBreakdown', () => {
  const fetchMock = vi.fn()
  beforeEach(() => { vi.stubGlobal('fetch', fetchMock); fetchMock.mockReset() })
  afterEach(() => { cleanup(); vi.unstubAllGlobals() })

  it('builds a bounded report path', () => {
    expect(reportPath(7, { from: 'a', to: 'b', breakdown: 'page', attribution: 'last_click', lookbackDays: 500 })).toBe(
      '/api/private/sites/7/revenue/report?from=a&to=b&breakdown=page&attribution=last_click&lookback_days=90')
  })

  it('shows metrics and rows, hides customer metrics when absent', async () => {
    fetchMock.mockImplementation(() => reply(200, report))
    mount()
    expect(await screen.findByText('$66.67')).toBeVisible()
    expect(screen.getByText('Direct or unknown')).toBeVisible()
    expect(screen.getByText('Revenue per visitor')).toBeVisible()
    expect(screen.queryByText('Unique customers')).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Customers' })).not.toBeInTheDocument()
    expect(fetchMock.mock.calls[0][1].headers.Accept).toBe('application/json')
  })

  it('refetches when breakdown and attribution change and disables lookback for event', async () => {
    fetchMock.mockImplementation(() => reply(200, report))
    mount()
    await screen.findByText('$66.67')
    const lookback = screen.getByLabelText('Lookback days')
    expect(lookback).toBeDisabled()
    fireEvent.change(screen.getByLabelText('Attribution'), { target: { value: 'first_click' } })
    fireEvent.change(screen.getByLabelText('Break down by'), { target: { value: 'country' } })
    await waitFor(() => expect(fetchMock.mock.calls.some(([url]) => String(url).includes('breakdown=country') && String(url).includes('attribution=first_click') && String(url).includes('lookback_days=30'))).toBe(true))
    expect(screen.getByLabelText('Lookback days')).toBeEnabled()
  })

  it('shows customer metrics and the customers table in identified mode', async () => {
    fetchMock.mockImplementation((url: string) => url.includes('/customers') ? reply(200, customers) : reply(200, identified))
    mount()
    expect((await screen.findAllByText('Unique customers')).length).toBe(2)
    expect(screen.getAllByText('25%').length).toBeGreaterThan(0)
    expect(await screen.findByText('cust-42')).toBeVisible()
  })

  it('handles forbidden customers gracefully', async () => {
    fetchMock.mockImplementation((url: string) => url.includes('/customers') ? reply(403, { detail: 'forbidden' }) : reply(200, identified))
    mount()
    expect(await screen.findByText(/needs the profiles permission/i)).toBeVisible()
    expect(screen.queryByLabelText('Search customers')).not.toBeInTheDocument()
  })

  it('shows truncated, empty, conflict and error states', async () => {
    fetchMock.mockImplementation(() => reply(200, { ...report, truncated: true, rows: [] }))
    mount()
    expect(await screen.findByText(/capped at 20,000 orders/i)).toBeVisible()
    expect(screen.getByText(/no orders in this period/i)).toBeVisible()
    cleanup()
    fetchMock.mockImplementation(() => reply(409, { detail: 'mixed' }))
    mount()
    expect(await screen.findByText(/more than one currency/i)).toBeVisible()
    cleanup()
    fetchMock.mockImplementation(() => reply(500, { detail: 'boom' }))
    mount()
    expect(await screen.findByText('boom')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeVisible()
  })

  it('exports csv with the csv accept header and reports a forbidden export', async () => {
    URL.createObjectURL = vi.fn(() => 'blob:x')
    URL.revokeObjectURL = vi.fn()
    fetchMock.mockImplementation((url: string, init: { headers: Record<string, string> }) => init.headers.Accept === 'text/csv' ? Promise.resolve(new Response('key,orders\n', { status: 200 })) : reply(200, report))
    mount()
    await screen.findByText('$66.67')
    fireEvent.click(screen.getByRole('button', { name: 'Export CSV' }))
    await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init.headers.Accept === 'text/csv')).toBe(true))
    await waitFor(() => expect(URL.createObjectURL).toHaveBeenCalled())
    fetchMock.mockImplementation((url: string, init: { headers: Record<string, string> }) => init.headers.Accept === 'text/csv' ? reply(403, { detail: 'no' }) : reply(200, report))
    fireEvent.click(screen.getByRole('button', { name: 'Export CSV' }))
    expect(await screen.findByText(/data export permission/i)).toBeVisible()
  })
})
