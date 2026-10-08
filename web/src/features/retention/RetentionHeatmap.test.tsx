import '@testing-library/jest-dom/vitest'
import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { RetentionHeatmap } from './RetentionHeatmap'

describe('RetentionHeatmap', () => {
  it('shows hand-computed counts and percentages as within-day session returns', () => {
    render(
      <RetentionHeatmap
        identityWindow="site-local day"
        retentionMode="within_day_sessions"
        cohorts={[
          { start: '2026-10-06', size: 10, retained: [10, 4, 1] },
          { start: '2026-10-07', size: 8, retained: [8, 2] },
        ]}
      />,
    )

    expect(screen.getByText(/site-local day/i)).toBeVisible()
    expect(screen.getByText(/cannot identify the same person across days/i)).toBeVisible()
    const table = screen.getByRole('table', { name: 'Within-day returning sessions' })
    expect(within(table).getByRole('columnheader', { name: 'Session 1' })).toBeVisible()
    expect(within(table).getByRole('columnheader', { name: 'Return 1' })).toBeVisible()
    expect(within(table).getByRole('cell', { name: '4 visitors, 40%' })).toBeVisible()
    expect(within(table).getByRole('cell', { name: '1 visitor, 10%' })).toBeVisible()
    expect(within(table).getByRole('cell', { name: '2 visitors, 25%' })).toBeVisible()
  })
})
