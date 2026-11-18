import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { useObsMetrics, useObsMetricQuery } = vi.hoisted(() => ({ useObsMetrics: vi.fn(), useObsMetricQuery: vi.fn() }))
vi.mock('./api', () => ({ useObsMetrics, useObsMetricQuery }))

import { MetricExplorer } from './MetricExplorer'
import { ObsMetricWidget } from './ObsMetricWidget'

const metrics = [
  { name: 'http.requests', kind: 'counter', unit: '1', series: 3, attribute_keys: ['region', 'service'] },
  { name: 'http.latency', kind: 'histogram', unit: 'ms', series: 2, attribute_keys: ['service'] },
]

async function choose(trigger: HTMLElement, option: string) {
  fireEvent.keyDown(trigger, { key: 'ArrowDown' })
  fireEvent.click(await screen.findByRole('option', { name: option }))
}

describe('MetricExplorer', () => {
  beforeEach(() => {
    Element.prototype.scrollIntoView = vi.fn()
    Element.prototype.hasPointerCapture = vi.fn(() => false)
    Element.prototype.releasePointerCapture = vi.fn()
    useObsMetrics.mockReturnValue({ data: metrics, isPending: false, isError: false })
    useObsMetricQuery.mockReturnValue({ data: undefined, isError: false, refetch: vi.fn() })
  })
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('builds the expected MetricQuery from the chosen metric, aggregation, group-by and filter', async () => {
    render(<MetricExplorer siteId={5} timezone="Asia/Kathmandu" />)
    expect(screen.getByText('Choose a metric to chart it.')).toBeVisible()
    expect(useObsMetricQuery).toHaveBeenLastCalledWith(5, null)

    await choose(screen.getByRole('combobox', { name: 'Metric' }), 'http.requests')
    await choose(screen.getByRole('combobox', { name: 'Aggregation' }), 'Rate per second')
    await choose(screen.getByRole('combobox', { name: 'Group by' }), 'service')
    fireEvent.click(screen.getByRole('button', { name: 'Add filter' }))
    fireEvent.change(screen.getByLabelText('Value'), { target: { value: 'eu' } })

    const query = useObsMetricQuery.mock.calls.at(-1)![1]
    expect(query).toMatchObject({
      name: 'http.requests', agg: 'rate', group_by: ['service'], filters: { region: 'eu' }, interval: '5m',
    })
    expect(Date.parse(query.to) - Date.parse(query.from)).toBe(24 * 3_600_000)
  })

  it('offers percentiles only for histograms', async () => {
    render(<MetricExplorer siteId={5} timezone="UTC" />)
    await choose(screen.getByRole('combobox', { name: 'Metric' }), 'http.latency')
    fireEvent.keyDown(screen.getByRole('combobox', { name: 'Aggregation' }), { key: 'ArrowDown' })
    const options = (await screen.findAllByRole('option')).map((option) => option.textContent)
    expect(options).toEqual(expect.arrayContaining(['p50', 'p95', 'p99']))
  })

  it('explains an empty project', () => {
    useObsMetrics.mockReturnValue({ data: [], isPending: false, isError: false })
    render(<MetricExplorer siteId={5} timezone="UTC" />)
    expect(screen.getByText(/No metrics yet/)).toBeVisible()
  })
})

describe('ObsMetricWidget', () => {
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('queries with the dashboard range and renders the series', () => {
    useObsMetricQuery.mockReturnValue({
      data: { series: [{ group: { service: 'api' }, points: [{ ts: '2026-11-17T18:15:00Z', value: 3 }, { ts: '2026-11-18T18:15:00Z', value: 7.5 }] }] },
      isError: false, refetch: vi.fn(),
    })
    render(<ObsMetricWidget chart="bar" dateRange={{ from: '2026-11-17', to: '2026-11-18' }} query={{ name: 'orders', agg: 'sum', interval: '1d', group_by: ['service'] }} siteId={5} timezone="Asia/Kathmandu" title="Orders" />)
    expect(useObsMetricQuery).toHaveBeenLastCalledWith(5, {
      name: 'orders', agg: 'sum', group_by: ['service'], interval: '1d', from: '2026-11-16T18:15:00.000Z', to: '2026-11-18T18:15:00.000Z',
    })
    const region = screen.getByRole('region', { name: 'Orders' })
    expect(within(region).getByRole('row', { name: /service=api.*7\.5/ })).toBeInTheDocument()
  })

  it('says so when the widget has no query', () => {
    useObsMetricQuery.mockReturnValue({ data: undefined, isError: false, refetch: vi.fn() })
    render(<ObsMetricWidget dateRange={{ from: '2026-11-17', to: '2026-11-18' }} siteId={5} timezone="UTC" title="Empty" />)
    expect(useObsMetricQuery).toHaveBeenLastCalledWith(5, null)
    expect(screen.getByText('This widget has no metric query.')).toBeVisible()
  })
})
