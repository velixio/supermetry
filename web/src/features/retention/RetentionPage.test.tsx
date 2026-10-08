import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { useRetentionReport } = vi.hoisted(() => ({ useRetentionReport: vi.fn() }))
vi.mock('./api', () => ({ useRetentionReport }))

import { RetentionPage } from './RetentionPage'

afterEach(cleanup)

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
        identity="anonymous"
        interval="day"
        profilesEnabled={true}
        siteId={7}
        onIdentityChange={() => undefined}
        onIntervalChange={onIntervalChange}
      />,
    )

    expect(screen.getByRole('heading', { name: 'Retention' })).toBeVisible()
    expect(screen.getByText(/cannot identify the same person across days/i)).toBeVisible()
    fireEvent.change(screen.getByLabelText('Group cohorts by'), { target: { value: 'week' } })
    expect(onIntervalChange).toHaveBeenCalledWith('week')
  })

  it('offers profile identity only when profiles are enabled and sends the selection', () => {
    useRetentionReport.mockReturnValue({
      data: {
        identity_window: 'profile',
        retention_mode: 'profile_return',
        cohorts: [{ start: '2026-10-08', size: 5, retained: [5, 2] }],
      },
      isPending: false,
      isLoading: false,
      isError: false,
    })
    const onIdentityChange = vi.fn()

    render(
      <RetentionPage
        dateRange={{ from: '2026-10-01', to: '2026-10-08' }}
        filters={[]}
        identity="profile"
        interval="day"
        profilesEnabled={true}
        siteId={7}
        onIdentityChange={onIdentityChange}
        onIntervalChange={() => undefined}
      />,
    )

    expect(screen.getByRole('option', { name: 'Identity profile' })).toBeVisible()
    expect(screen.getByText(/identity window: profile/i)).toBeVisible()
    expect(useRetentionReport).toHaveBeenCalledWith(7, { from: '2026-10-01', to: '2026-10-08' }, [], 'day', 'profile')
    fireEvent.change(screen.getByLabelText('Identify visitors by'), { target: { value: 'anonymous' } })
    expect(onIdentityChange).toHaveBeenCalledWith('anonymous')
  })
})
