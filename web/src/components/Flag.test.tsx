import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { Flag } from './Flag'

describe('Flag', () => {
  afterEach(cleanup)

  it('renders a lowercase flag asset for a valid ISO country code', () => {
    render(<Flag code="in" />)

    const flag = screen.getByRole('presentation', { hidden: true })
    expect(flag).toHaveAttribute('src', '/flags/in.svg')
    expect(flag).toHaveAttribute('alt', '')
    expect(flag).toHaveAttribute('aria-hidden', 'true')
    expect(flag).toHaveAttribute('loading', 'lazy')
    expect(flag).toHaveAttribute('width', '20')
    expect(flag).toHaveAttribute('height', '15')
  })

  it('uses a neutral globe for invalid codes and failed images', () => {
    const { rerender } = render(<Flag code="invalid" />)
    expect(screen.getByTestId('flag-fallback')).toBeVisible()

    rerender(<Flag code="ZZ" />)
    fireEvent.error(screen.getByRole('presentation', { hidden: true }))
    expect(screen.getByTestId('flag-fallback')).toBeVisible()
  })
})
