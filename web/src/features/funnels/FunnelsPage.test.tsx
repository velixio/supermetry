import '@testing-library/jest-dom/vitest'

import { fireEvent, render, screen } from '@testing-library/react'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

const { useFunnelResult } = vi.hoisted(() => ({ useFunnelResult: vi.fn() }))

vi.mock('./api', () => ({
  useFunnels: () => ({ data: [] }),
  useCreateFunnel: () => ({ isPending: false, mutateAsync: vi.fn() }),
  useUpdateFunnel: () => ({ isPending: false, mutateAsync: vi.fn() }),
  useFunnelResult,
}))

import { FunnelsPage } from './FunnelsPage'

describe('FunnelsPage', () => {
  beforeAll(() => {
    Element.prototype.scrollIntoView = vi.fn()
  })

  beforeEach(() => {
    useFunnelResult.mockReset().mockReturnValue({ data: undefined })
  })

  it('counts sessions by default and can switch to visitors', () => {
    render(<FunnelsPage siteId={7} dateRange={{ from: '2026-10-01', to: '2026-10-07' }} />)

    expect(screen.queryByRole('main')).not.toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Count by' })).toHaveTextContent('Sessions')
    expect(useFunnelResult).toHaveBeenLastCalledWith(
      7,
      { from: '2026-10-01', to: '2026-10-07' },
      [],
      'sessions',
      'session',
    )
    expect(screen.getByText(/anonymous visitors only link within one site-local day/i)).toBeVisible()

    fireEvent.click(screen.getByRole('combobox', { name: 'Count by' }))
    fireEvent.click(screen.getByRole('option', { name: 'Visitors' }))

    expect(useFunnelResult).toHaveBeenLastCalledWith(
      7,
      { from: '2026-10-01', to: '2026-10-07' },
      [],
      'visitors',
      'session',
    )

    fireEvent.click(screen.getByRole('combobox', { name: 'Conversion window' }))
    fireEvent.click(screen.getByRole('option', { name: '24 hours' }))
    expect(useFunnelResult).toHaveBeenLastCalledWith(
      7,
      { from: '2026-10-01', to: '2026-10-07' },
      [],
      'visitors',
      '24h',
    )
  })
})
