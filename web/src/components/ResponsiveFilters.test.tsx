import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ResponsiveFilters } from './ResponsiveFilters'

afterEach(cleanup)

describe('ResponsiveFilters', () => {
  it('collapses all mobile controls into one labelled sheet', () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 })
    Object.defineProperty(window, 'matchMedia', { configurable: true, value: vi.fn().mockReturnValue({ addEventListener: vi.fn(), removeEventListener: vi.fn() }) })
    render(<ResponsiveFilters><button type="button">Date range</button><button type="button">Include imported data</button></ResponsiveFilters>)

    fireEvent.click(screen.getByRole('button', { name: 'Filters' }))
    const sheet = screen.getByRole('dialog', { name: 'Filters' })
    expect(sheet).toContainElement(screen.getByRole('button', { name: 'Date range' }))
    expect(sheet).toContainElement(screen.getByRole('button', { name: 'Include imported data' }))
  })
})
