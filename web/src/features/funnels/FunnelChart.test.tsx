import '@testing-library/jest-dom/vitest'
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { FunnelChart } from './FunnelChart'

describe('FunnelChart', () => {
  it('computes each step conversion from the preceding session count', () => {
    render(
      <FunnelChart
        countLabel="Sessions"
        steps={[
          { label: 'Landing', count: 100 },
          { label: 'Signup', count: 40 },
          { label: 'Purchase', count: 10 },
        ]}
      />,
    )

    expect(screen.getAllByText('40%')).not.toHaveLength(0)
    expect(screen.getAllByText('25%')).not.toHaveLength(0)
    expect(screen.getAllByText('60% drop-off')).not.toHaveLength(0)
    expect(screen.getAllByText('75% drop-off')).not.toHaveLength(0)
    expect(screen.getByRole('columnheader', { name: 'Sessions' })).toBeVisible()
    expect(screen.getByText(/Funnel step sessions and conversion/i)).toBeInTheDocument()
  })
})
