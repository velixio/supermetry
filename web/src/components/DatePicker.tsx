import type { ChangeEvent } from 'react'

import {
  compareValues,
  periodValues,
  type Compare,
  type DashboardSearch,
  type Period,
} from '../lib/search'

export type DatePickerLabels = {
  period: string
  periods: Record<Period, string>
  from: string
  to: string
  compare: string
  comparisons: Record<Compare, string>
}

type DatePickerProps = {
  search: DashboardSearch
  labels: DatePickerLabels
  onChange: (search: DashboardSearch) => void
}

const controlClass =
  'h-9 rounded-md border bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring'

export function DatePicker({ search, labels, onChange }: DatePickerProps) {
  const setPeriod = (event: ChangeEvent<HTMLSelectElement>) => {
    onChange({ ...search, period: event.target.value as Period })
  }
  const setCompare = (event: ChangeEvent<HTMLSelectElement>) => {
    onChange({ ...search, compare: event.target.value as Compare })
  }
  return (
    <div className="flex flex-wrap items-center gap-2">
      <select
        aria-label={labels.period}
        className={controlClass}
        value={search.period}
        onChange={setPeriod}
      >
        {periodValues.map((period) => (
          <option key={period} value={period}>
            {labels.periods[period]}
          </option>
        ))}
      </select>
      {search.period === 'custom' ? (
        <>
          <input
            aria-label={labels.from}
            className={controlClass}
            type="date"
            value={search.from ?? ''}
            onChange={(event) =>
              onChange({
                ...search,
                from: event.target.value || undefined,
              })
            }
          />
          <input
            aria-label={labels.to}
            className={controlClass}
            type="date"
            value={search.to ?? ''}
            onChange={(event) =>
              onChange({ ...search, to: event.target.value || undefined })
            }
          />
        </>
      ) : null}
      <label className="flex items-center gap-2 text-sm font-medium">
        <span>{labels.compare}</span>
        <select
          className={controlClass}
          value={search.compare}
          onChange={setCompare}
        >
          {compareValues.map((compare) => (
            <option key={compare} value={compare}>
              {labels.comparisons[compare]}
            </option>
          ))}
        </select>
      </label>
    </div>
  )
}
