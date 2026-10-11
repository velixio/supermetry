import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { CountryTable } from './CountryTable'

const rows = ['US', 'IN', 'DE', 'FR', 'GB', 'CA', 'AU', 'BR', 'JP', 'ZA'].map((country, index) => ({
  country,
  visitors: 100 - index * 5,
  pageviews: 200 - index * 10,
}))

describe('CountryTable', () => {
  afterEach(cleanup)

  it('shows the top six with rank, value, share bar, and expands in place', () => {
    render(<CountryTable rows={rows} metric="visitors" onActiveChange={vi.fn()} onSelect={vi.fn()} />)

    expect(screen.getAllByRole('button', { name: /rank \d+/i })).toHaveLength(6)
    expect(screen.getByRole('button', { name: /United States, rank 1, 100 visitors/i })).toBeVisible()
    expect(screen.getByLabelText('United States share')).toHaveAttribute('data-share')
    fireEvent.click(screen.getByRole('button', { name: 'Show all countries' }))
    expect(screen.getAllByRole('button', { name: /rank \d+/i })).toHaveLength(10)
    expect(screen.getByRole('button', { name: 'Show fewer countries' })).toBeVisible()
  })

  it('links row hover and focus and marks selected and externally active rows', () => {
    const onActiveChange = vi.fn()
    const onSelect = vi.fn()
    const { rerender } = render(
      <CountryTable rows={rows.slice(0, 2)} metric="visitors" selectedCountry="IN" onActiveChange={onActiveChange} onSelect={onSelect} />,
    )
    const india = screen.getByRole('button', { name: /India, rank 2/i })
    expect(india).toHaveAttribute('aria-pressed', 'true')
    fireEvent.mouseEnter(india)
    expect(onActiveChange).toHaveBeenLastCalledWith('IN')
    fireEvent.focus(india)
    expect(onActiveChange).toHaveBeenLastCalledWith('IN')
    fireEvent.click(india)
    expect(onSelect).toHaveBeenCalledWith('IN')

    rerender(<CountryTable rows={rows.slice(0, 2)} metric="visitors" activeCountry="US" onActiveChange={onActiveChange} onSelect={onSelect} />)
    expect(screen.getByRole('button', { name: /United States, rank 1/i })).toHaveAttribute('data-active', 'true')
  })
})
