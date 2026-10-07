import '@testing-library/jest-dom/vitest'
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { FunnelChart } from './FunnelChart'

describe('FunnelChart', () => {
  it('computes each step conversion from the preceding visitor count', () => {
    render(
      <FunnelChart
        steps={[
          { label: 'Landing', visitors: 100 },
          { label: 'Signup', visitors: 40 },
          { label: 'Purchase', visitors: 10 },
        ]}
      />,
    )

    expect(screen.getAllByText('40%')).not.toHaveLength(0)
    expect(screen.getAllByText('25%')).not.toHaveLength(0)
    expect(screen.getAllByText('60% drop-off')).not.toHaveLength(0)
    expect(screen.getAllByText('75% drop-off')).not.toHaveLength(0)
  })
})
