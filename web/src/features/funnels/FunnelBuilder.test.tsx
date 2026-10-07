import '@testing-library/jest-dom/vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { FunnelBuilder } from './FunnelBuilder'

describe('FunnelBuilder', () => {
  it('rejects fewer than two steps and empty step values', () => {
    const onSubmit = vi.fn()
    render(<FunnelBuilder onSubmit={onSubmit} />)

    fireEvent.click(screen.getByRole('button', { name: 'Save funnel' }))
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Add at least two steps.',
    )
    expect(onSubmit).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Add step' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save funnel' }))
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Every step needs a value.',
    )
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('loads steps when a saved funnel arrives', () => {
    const onSubmit = vi.fn()
    const { rerender } = render(<FunnelBuilder onSubmit={onSubmit} />)

    rerender(
      <FunnelBuilder
        initialSteps={[
          { type: 'page', match: 'exact', value: '/' },
          { type: 'event', match: 'prefix', value: 'signup' },
        ]}
        onSubmit={onSubmit}
      />,
    )

    expect(screen.getByDisplayValue('/')).toBeVisible()
    expect(screen.getByDisplayValue('signup')).toBeVisible()
  })
})
