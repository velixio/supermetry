import '@testing-library/jest-dom/vitest'

import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { EmptyState } from './EmptyState'

describe('EmptyState', () => {
  it('explains the empty result and gives a next action', () => {
    render(<EmptyState />)

    expect(screen.getByText('No data for this date range.')).toBeVisible()
    expect(screen.getByText('Try a wider date range or remove a filter.')).toBeVisible()
  })
})
