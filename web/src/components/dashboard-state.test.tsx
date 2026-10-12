import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'

import { DatePicker, type DatePickerLabels } from './DatePicker'
import { FilterBar } from './FilterBar'
import { LiveBadge } from './LiveBadge'
import { dashboardSearch } from '../lib/search'

const labels: DatePickerLabels = {
  period: 'Date range',
  periods: {
    today: 'Today',
    yesterday: 'Yesterday',
    '7d': 'Last 7 days',
    '28d': 'Last 28 days',
    '30d': 'Last 30 days',
    '91d': 'Last 91 days',
    '6mo': 'Last 6 months',
    '12mo': 'Last 12 months',
    month: 'This month',
    last_month: 'Last month',
    year: 'This year',
    custom: 'Custom',
  },
  from: 'From',
  to: 'To',
  compare: 'Compare',
  comparisons: {
    off: 'No comparison',
    previous_period: 'Previous period',
    previous_year: 'Previous year',
  },
}

beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn()
})

afterEach(cleanup)

describe('DatePicker', () => {
  it('lists every period in calendar order', () => {
    render(
      <DatePicker
        search={dashboardSearch.parse({})}
        labels={labels}
        onChange={vi.fn()}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: /date range/i }))
    fireEvent.click(screen.getByRole('combobox', { name: 'Preset' }))
    expect(screen.getAllByRole('option').map((option) => option.textContent)).toEqual([
      'Today',
      'Yesterday',
      'Last 7 days',
      'Last 28 days',
      'Last 30 days',
      'Last 91 days',
      'Last 6 months',
      'Last 12 months',
      'This month',
      'Last month',
      'This year',
      'Custom',
    ])
  })

  it('shows a visible Compare label and all comparison choices', () => {
    render(
      <DatePicker
        search={dashboardSearch.parse({})}
        labels={labels}
        onChange={vi.fn()}
      />,
    )

    expect(screen.getByText('Compare')).toBeVisible()
    fireEvent.click(screen.getByRole('combobox', { name: 'Compare' }))
    expect(screen.getByRole('option', { name: 'No comparison' })).toBeVisible()
    expect(screen.getByRole('option', { name: 'Previous period' })).toBeVisible()
    expect(screen.getByRole('option', { name: 'Previous year' })).toBeVisible()
  })

  it('emits period and compare changes while preserving filters', () => {
    const onChange = vi.fn()
    const search = dashboardSearch.parse({ f: ['country:is:IN'] })
    render(
      <DatePicker search={search} labels={labels} onChange={onChange} />,
    )

    fireEvent.click(screen.getByRole('button', { name: /date range/i }))
    fireEvent.click(screen.getByRole('combobox', { name: 'Preset' }))
    fireEvent.click(screen.getByRole('option', { name: 'Last 7 days' }))
    expect(onChange).toHaveBeenLastCalledWith({ ...search, period: '7d' })
    fireEvent.click(screen.getByRole('combobox', { name: 'Compare' }))
    fireEvent.click(screen.getByRole('option', { name: 'Previous year' }))
    expect(onChange).toHaveBeenLastCalledWith({
      ...search,
      compare: 'previous_year',
    })
  })

  it.each([
    'today',
    'yesterday',
    '7d',
    '28d',
    '30d',
    '91d',
    '6mo',
    '12mo',
    'month',
    'last_month',
    'year',
    'custom',
  ] as const)(
    'sets the %s search period while preserving other state',
    (period) => {
      const onChange = vi.fn()
      const search = dashboardSearch.parse({
        period: period === '30d' ? '7d' : '30d',
        f: ['country:is:IN'],
        compare: 'previous_period',
      })
      render(
        <DatePicker search={search} labels={labels} onChange={onChange} />,
      )

      fireEvent.click(screen.getByRole('button', { name: /date range/i }))
      fireEvent.click(screen.getByRole('combobox', { name: 'Preset' }))
      fireEvent.click(screen.getByRole('option', { name: labels.periods[period] }))

      expect(onChange).toHaveBeenCalledWith({ ...search, period })
    },
  )

  it('selects a custom range from the calendar while preserving filters', () => {
    const onChange = vi.fn()
    const search = dashboardSearch.parse({
      period: 'custom',
      from: '2026-10-01',
      to: '2026-10-07',
      f: ['country:is:IN'],
    })
    render(<DatePicker search={search} labels={labels} onChange={onChange} />)

    fireEvent.click(screen.getByRole('button', { name: /date range/i }))
    fireEvent.click(screen.getByRole('button', { name: 'Choose October 10, 2026' }))
    expect(onChange).toHaveBeenLastCalledWith({ ...search, period: 'custom', from: '2026-10-10', to: undefined })

    fireEvent.click(screen.getByRole('button', { name: 'Choose October 12, 2026' }))
    expect(onChange).toHaveBeenLastCalledWith({ ...search, period: 'custom', from: '2026-10-10', to: '2026-10-12' })
  })
})

describe('FilterBar', () => {
  it('shows one invalid-filter notice and removes a chip by index', () => {
    const onRemove = vi.fn()
    const client = new QueryClient({ defaultOptions: { queries: { enabled: false } } })
    render(
      <QueryClientProvider client={client}>
        <FilterBar
          siteId={7}
          canEdit
          filters={[
            { dimension: 'country', operator: 'is', values: ['IN'] },
          ]}
          dropped={['bad', 'also-bad']}
          invalidNotice="Some filters were removed"
          filterLabel={(filter) => filter.values.join(', ')}
          removeLabel={(filter) => `Remove ${filter.values.join(', ')}`}
          onApply={vi.fn()}
          onRemove={onRemove}
        />
      </QueryClientProvider>,
    )

    expect(screen.getAllByRole('status')).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: 'Remove IN' }))
    expect(onRemove).toHaveBeenCalledWith(0)
  })
})

describe('LiveBadge', () => {
  it('exposes visitor count and toggle state without relying on color', () => {
    const onToggle = vi.fn()
    render(
      <LiveBadge
        count={3}
        enabled
        label={(count) => `${count} visitors now`}
        disableLabel="Pause live updates"
        enableLabel="Resume live updates"
        onToggle={onToggle}
      />,
    )

    const button = screen.getByRole('button', { name: 'Pause live updates' })
    expect(button.getAttribute('aria-pressed')).toBe('true')
    expect(screen.getByText('3 visitors now')).toBeTruthy()
    fireEvent.click(button)
    expect(onToggle).toHaveBeenCalledOnce()
  })
})

describe('imported data search state', () => {
  it('includes imported data by default and preserves an explicit toggle', () => {
    expect(dashboardSearch.parse({}).include_imported).toBe(true)
    expect(dashboardSearch.parse({ include_imported: false }).include_imported).toBe(false)
    expect(dashboardSearch.parse({ include_imported: 'false' }).include_imported).toBe(false)
  })
})
