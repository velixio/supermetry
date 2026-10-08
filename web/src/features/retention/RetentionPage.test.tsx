import '@testing-library/jest-dom/vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

const { useRetentionReport } = vi.hoisted(() => ({ useRetentionReport: vi.fn() }))
vi.mock('./api', () => ({ useRetentionReport }))

import { RetentionPage } from './RetentionPage'

describe('RetentionPage', () => {
  it('renders within-day cohorts and changes the grouping interval', () => {
    useRetentionReport.mockReturnValue({
      data: {
        identity_window: 'day',
        retention_mode: 'within_day_session_return',
        cohorts: [{ start: '2026-10-08', size: 5, retained: [5, 2] }],
      },
      isPending: false,
      isLoading: false,
      isError: false,
    })
    const onIntervalChange = vi.fn()

    render(
      <RetentionPage
        dateRange={{ from: '2026-10-01', to: '2026-10-08' }}
        filters={[]}
        interval="day"
        siteId={7}
        onIntervalChange={onIntervalChange}
      />,
    )

    expect(screen.getByRole('heading', { name: 'Retention' })).toBeVisible()
    expect(screen.getByText(/cannot identify the same person across days/i)).toBeVisible()
    fireEvent.change(screen.getByLabelText('Group cohorts by'), { target: { value: 'week' } })
    expect(onIntervalChange).toHaveBeenCalledWith('week')
  })
})
