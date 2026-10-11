import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'

import { BrandLogo } from './BrandLogo'

afterEach(cleanup)

it('keeps duplicated brand artwork out of the accessibility tree', () => {
  render(
    <a aria-label="Supermetry home" href="/app">
      <BrandLogo mark />
      <span>Supermetry</span>
    </a>,
  )

  expect(screen.getByRole('link', { name: 'Supermetry home' })).toBeVisible()
  expect(screen.queryByRole('img')).not.toBeInTheDocument()
})
