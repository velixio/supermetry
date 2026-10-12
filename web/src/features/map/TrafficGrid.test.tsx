import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { TrafficGrid } from './TrafficGrid'

describe('TrafficGrid', () => {
  afterEach(cleanup)

  it('renders all cells, readable margins, peak windows, weekday share, a legend, and a portalled tooltip', () => {
    render(<TrafficGrid dateRange={{ from: '2026-09-28', to: '2026-09-30' }} metric="visitors" rows={[
      { weekday: 1, hour_of_day: 13, visitors: 89 },
      { weekday: 2, hour_of_day: 13, visitors: 40 },
    ]} timezone="America/New_York" locale="en-GB" />)

    expect(screen.getAllByRole('gridcell').filter((cell) => cell.tagName === 'BUTTON')).toHaveLength(168)
    expect(screen.getByText('Busiest: Monday 13:00, 89 visitors')).toBeVisible()
    expect(screen.getByText('Quietest weekday: Wednesday')).toBeVisible()
    expect(screen.getByLabelText('Monday total: 89 visitors')).toBeVisible()
    expect(screen.getByLabelText('13:00 total: 129 visitors')).toBeVisible()
    expect(screen.getByLabelText('Traffic intensity legend').children).toHaveLength(5)
    expect(screen.getByRole('heading', { name: 'Peak windows' })).toBeVisible()
    expect(screen.getByText('Monday 13:00, 89 visitors')).toBeVisible()
    expect(screen.getByText('Tuesday 13:00, 40 visitors')).toBeVisible()
    expect(screen.getByText('Weekdays 100%')).toBeVisible()
    expect(screen.getByText('Weekends 0%')).toBeVisible()
    const peak = screen.getByRole('gridcell', { name: 'Monday 13:00: 89 visitors' })
    expect(peak).toHaveAttribute('data-busiest', 'true')
    fireEvent.focus(peak)
    expect(screen.getByRole('tooltip')).toHaveTextContent('Monday13:0089 visitors')
    expect(screen.getByRole('tooltip').parentElement).toBe(document.body)
    expect(screen.getByRole('tooltip')).toHaveClass('pointer-events-none')
  })

  it('describes the range, total, peak, and low and relates the interactive grid to it', () => {
    render(<TrafficGrid dateRange={{ from: '2026-09-28', to: '2026-09-30' }} metric="visitors" rows={[
      { weekday: 1, hour_of_day: 13, visitors: 89 },
      { weekday: 2, hour_of_day: 13, visitors: 40 },
    ]} timezone="America/New_York" locale="en-GB" />)

    const summary = screen.getByText(
      'Weekly rhythm from 28 Sept 2026 to 30 Sept 2026. Total 129 visitors. Peak 89 on Monday at 13:00. Low 0 on Monday at 00:00.',
    )
    expect(summary).toHaveClass('sr-only')
    expect(screen.getAllByText('Busiest: Monday 13:00, 89 visitors')).toHaveLength(1)
    const grid = screen.getByRole('grid', { name: 'Weekly traffic by weekday and hour' })
    expect(grid).toHaveAttribute('aria-describedby', summary.id)
    expect(screen.getByRole('table', { name: 'Weekly traffic data' })).toBeInTheDocument()
  })

  it('uses no label, value, or tick token smaller than 12px and gives rows and hourly bars room', () => {
    const { container } = render(<TrafficGrid metric="visitors" rows={[]} timezone="UTC" locale="en-GB" />)
    const classNames = [...container.querySelectorAll<HTMLElement>('[class]')].map((node) => node.className).join(' ')

    expect(classNames).not.toMatch(/text-\[(?:8|9|10|11)px\]/)
    expect(container.querySelector('[role="row"][data-weekday-row]')).toHaveClass('min-h-7')
    expect(screen.getByRole('rowheader', { name: 'Hourly totals' }).parentElement).toHaveClass('min-h-10')
  })

  it('has one tab stop and supports arrows plus Home and End within weekday rows', () => {
    render(<TrafficGrid metric="pageviews" rows={[]} timezone="UTC" locale="en-US" />)
    const sundayMidnight = screen.getByRole('gridcell', { name: 'Sunday 12 AM: 0 pageviews' })
    const sundayOne = screen.getByRole('gridcell', { name: 'Sunday 1 AM: 0 pageviews' })
    const mondayMidnight = screen.getByRole('gridcell', { name: 'Monday 12 AM: 0 pageviews' })

    expect(screen.getAllByRole('gridcell').filter((cell) => cell.tabIndex === 0)).toHaveLength(1)
    sundayMidnight.focus()
    fireEvent.keyDown(sundayMidnight, { key: 'ArrowRight' })
    expect(sundayOne).toHaveFocus()
    fireEvent.keyDown(sundayOne, { key: 'ArrowDown' })
    const mondayOne = screen.getByRole('gridcell', { name: 'Monday 1 AM: 0 pageviews' })
    expect(mondayOne).toHaveFocus()
    fireEvent.keyDown(mondayOne, { key: 'Home' })
    expect(mondayMidnight).toHaveFocus()
    fireEvent.keyDown(mondayMidnight, { key: 'End' })
    expect(screen.getByRole('gridcell', { name: 'Monday 11 PM: 0 pageviews' })).toHaveFocus()
  })

  it('shows a tapped cell value in the summary line', () => {
    render(<TrafficGrid metric="visitors" rows={[{ weekday: 1, hour_of_day: 13, visitors: 89 }]} timezone="UTC" locale="en-US" />)
    fireEvent.pointerDown(screen.getByRole('gridcell', { name: 'Monday 1 PM: 89 visitors' }), { pointerType: 'touch' })
    expect(screen.getByText('Monday 1 PM: 89 visitors')).toBeVisible()
  })

  it('reserves the weekly strip height while loading and renders the empty state', () => {
    const { rerender } = render(<TrafficGrid isLoading metric="visitors" rows={[]} timezone="UTC" />)
    expect(screen.getByLabelText('Loading traffic by hour')).toHaveClass('min-h-56')
    rerender(<TrafficGrid metric="visitors" rows={[]} timezone="UTC" />)
    expect(screen.getByText('No traffic in this range')).toBeVisible()
  })

  it('turns retention errors into the longest allowed range action', () => {
    const onShorten = vi.fn()
    render(<TrafficGrid error={{ detail: "Traffic breakdowns are available only within the site's 90-day raw retention window" }} metric="visitors" rows={[]} timezone="UTC" onShorten={onShorten} />)
    fireEvent.click(screen.getByRole('button', { name: 'Use last 90 days' }))
    expect(onShorten).toHaveBeenCalledWith(90)
  })

  it('shows a retry action for an ordinary error', () => {
    const onRetry = vi.fn()
    render(<TrafficGrid error={{ detail: 'Request failed' }} metric="visitors" rows={[]} timezone="UTC" onRetry={onRetry} />)
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(onRetry).toHaveBeenCalledOnce()
  })
})
