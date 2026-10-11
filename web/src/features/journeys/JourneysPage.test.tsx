import '@testing-library/jest-dom/vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

const { useJourneyReport, useStats } = vi.hoisted(() => ({
  useJourneyReport: vi.fn(),
  useStats: vi.fn(),
}))

vi.mock('./api', () => ({ useJourneyReport }))
vi.mock('@/lib/stats', () => ({ useStats }))

import { JourneysPage } from './JourneysPage'

describe('JourneysPage', () => {
  it('uses top pages for the picker and renders the report flow', () => {
    useStats.mockReturnValue({
      data: { rows: [{ page: '/pricing', visitors: 8 }] },
      isPending: false,
      isLoading: false,
      isError: false,
    })
    useJourneyReport.mockReturnValue({
      data: {
        nodes: [
          { step: 0, path: '/', visitors: 8 },
          { step: 1, path: '/pricing', visitors: 6 },
          { step: 1, path: 'exit', visitors: 2 },
        ],
        links: [
          { from_step: 0, from_path: '/', to_path: '/pricing', visitors: 6 },
          { from_step: 0, from_path: '/', to_path: 'exit', visitors: 2 },
        ],
      },
      isPending: false,
      isLoading: false,
      isError: false,
    })
    const onControlsChange = vi.fn()

    render(
      <JourneysPage
        dateRange={{ from: '2026-10-01', to: '2026-10-08' }}
        direction="start"
        filters={[]}
        path="/"
        siteId={7}
        steps={3}
        onControlsChange={onControlsChange}
      />,
    )

    expect(screen.queryByRole('main')).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'User journeys' })).toBeVisible()
    expect(screen.getByRole('img', { name: 'User journey path flow' })).toBeVisible()
    expect(document.querySelector('datalist option[value="/pricing"]')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Steps'), { target: { value: '5' } })
    expect(onControlsChange).toHaveBeenCalledWith({ direction: 'start', path: '/', steps: 5 })
  })
})
