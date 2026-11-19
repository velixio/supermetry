import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { ScreenReaderTable } from './ScreenReaderTable'

describe('ScreenReaderTable', () => {
  it('keeps the table for assistive tech inside a clipped sr-only box', () => {
    render(
      <ScreenReaderTable aria-label="Weekly traffic data">
        <tbody><tr><td>Monday</td></tr></tbody>
      </ScreenReaderTable>,
    )
    const table = screen.getByRole('table', { name: 'Weekly traffic data' })
    // A table ignores height: 1px and overflow: hidden, so sr-only on the table
    // itself still adds its full height to the page's scroll area.
    expect(table.classList.contains('sr-only')).toBe(false)
    expect(table.parentElement?.tagName).toBe('DIV')
    expect(table.parentElement?.classList.contains('sr-only')).toBe(true)
  })
})
