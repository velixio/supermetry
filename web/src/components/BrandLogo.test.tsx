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

it('names a standalone logo once, as a single inline svg', () => {
  const { container } = render(<BrandLogo />)

  expect(screen.getAllByRole('img')).toHaveLength(1)
  expect(screen.getByRole('img', { name: 'Supermetry' })).toBeInTheDocument()
  expect(container.querySelectorAll('img')).toHaveLength(0)
})

it('maps size presets to mark pixels and swaps to the simple mark at 20px and below', () => {
  const { container } = render(
    <>
      <BrandLogo mark size="xs" />
      <BrandLogo mark size="sm" />
      <BrandLogo mark size="md" />
      <BrandLogo mark size="lg" />
    </>,
  )
  const svgs = [...container.querySelectorAll('svg')]

  expect(svgs.map((s) => s.getAttribute('width'))).toEqual(['16', '20', '24', '32'])
  expect(svgs.map((s) => s.getAttribute('viewBox'))).toEqual(['0 0 16 16', '0 0 16 16', '0 0 32 32', '0 0 32 32'])
})

it('sizes the full logo by height and gives each instance its own gradient id', () => {
  const { container } = render(
    <>
      <BrandLogo size="lg" />
      <BrandLogo className="h-6 w-auto" />
    </>,
  )
  const svgs = [...container.querySelectorAll('svg')]
  const ids = [...container.querySelectorAll('linearGradient')].map((g) => g.id)

  expect(svgs[0]).toHaveAttribute('height', '32')
  expect(svgs[0]).toHaveAttribute('viewBox', '0 0 140 32')
  expect(svgs[1]).toHaveClass('h-6')
  expect(new Set(ids).size).toBe(2)
})
