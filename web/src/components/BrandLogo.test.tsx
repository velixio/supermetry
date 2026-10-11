import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'

import { BrandLogo } from './BrandLogo'

afterEach(cleanup)

it('keeps decorative brand artwork out of the accessibility tree', () => {
  render(
    <a aria-label="Supermetry home" href="/app">
      <BrandLogo decorative mark />
      <span>Supermetry</span>
    </a>,
  )

  expect(screen.getByRole('link', { name: 'Supermetry home' })).toBeVisible()
  expect(screen.queryByRole('img')).not.toBeInTheDocument()
})

it('names a standalone logo once and hides the dark-theme duplicate', () => {
  render(<BrandLogo />)

  expect(screen.getAllByRole('img')).toHaveLength(1)
  expect(screen.getByRole('img', { name: 'Supermetry' })).toBeInTheDocument()
})
