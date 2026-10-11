import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { TrafficGrid } from './TrafficGrid'

describe('TrafficGrid', () => {
  afterEach(cleanup)

  it('renders all cells, a peak summary, and a focus tooltip', () => {
    render(
      <TrafficGrid
        metric="visitors"
        rows={[{ weekday: 5, hour_of_day: 19, visitors: 164 }]}
        timezone="America/New_York"
        locale="en-US"
      />,
    )

    expect(screen.getAllByRole('gridcell')).toHaveLength(168)
    expect(screen.getByText('Busiest: Friday 7 PM, 164 visitors')).toBeVisible()
    const peak = screen.getByRole('gridcell', {
      name: 'Friday 7 PM: 164 visitors',
    })
    fireEvent.focus(peak)
    expect(screen.getByRole('tooltip')).toHaveTextContent('Friday7 PM164 visitors')
  })

  it('has one tab stop and wraps arrow-key focus in both axes', () => {
    render(<TrafficGrid metric="pageviews" rows={[]} timezone="UTC" locale="en-US" />)
    const sundayMidnight = screen.getByRole('gridcell', {
      name: 'Sunday 12 AM: 0 pageviews',
    })
    const saturdayMidnight = screen.getByRole('gridcell', {
      name: 'Saturday 12 AM: 0 pageviews',
    })
    const sundayEleven = screen.getByRole('gridcell', {
      name: 'Sunday 11 PM: 0 pageviews',
    })

    expect(screen.getAllByRole('gridcell').filter((cell) => cell.tabIndex === 0)).toHaveLength(1)
    sundayMidnight.focus()
    fireEvent.keyDown(sundayMidnight, { key: 'ArrowLeft' })
    expect(saturdayMidnight).toHaveFocus()
    fireEvent.keyDown(saturdayMidnight, { key: 'ArrowRight' })
    expect(sundayMidnight).toHaveFocus()
    fireEvent.keyDown(sundayMidnight, { key: 'ArrowUp' })
    expect(sundayEleven).toHaveFocus()
  })

  it('shows loading and empty states', () => {
    const { rerender } = render(<TrafficGrid isLoading metric="visitors" rows={[]} timezone="UTC" />)
    expect(screen.getByLabelText('Loading traffic by hour')).toBeVisible()

    rerender(<TrafficGrid metric="visitors" rows={[]} timezone="UTC" />)
    expect(screen.getByText('No traffic in this range')).toBeVisible()
  })

  it('turns the retention problem into an actionable shorter-range message', () => {
    const onShorten = vi.fn()
    render(
      <TrafficGrid
        error={{
          detail: "Traffic breakdowns are available only within the site's 7-day raw retention window",
        }}
        metric="visitors"
        rows={[]}
        timezone="UTC"
        onRetry={vi.fn()}
        onShorten={onShorten}
      />,
    )

    expect(
      screen.getByText('This range is longer than the 7 days of raw data kept for this site. Choose a shorter range.'),
    ).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Use last 7 days' }))
    expect(onShorten).toHaveBeenCalledWith(7)
  })

  it('shows a retry action for an ordinary error', () => {
    const onRetry = vi.fn()
    render(
      <TrafficGrid error={{ detail: 'Request failed' }} metric="visitors" rows={[]} timezone="UTC" onRetry={onRetry} />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(onRetry).toHaveBeenCalledOnce()
  })
})
