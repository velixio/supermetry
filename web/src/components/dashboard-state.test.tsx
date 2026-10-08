import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

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
    '30d': 'Last 30 days',
    month: 'This month',
    last_month: 'Last month',
    '12mo': 'Last 12 months',
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

afterEach(cleanup)

describe('DatePicker', () => {
  it('shows a visible Compare label and all comparison choices', () => {
    render(
      <DatePicker
        search={dashboardSearch.parse({})}
        labels={labels}
        onChange={vi.fn()}
      />,
    )

    expect(screen.getByText('Compare')).toBeVisible()
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

    fireEvent.change(screen.getByLabelText('Date range'), {
      target: { value: '7d' },
    })
    expect(onChange).toHaveBeenLastCalledWith({ ...search, period: '7d' })
    fireEvent.change(screen.getByLabelText('Compare'), {
      target: { value: 'previous_year' },
    })
    expect(onChange).toHaveBeenLastCalledWith({
      ...search,
      compare: 'previous_year',
    })
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
