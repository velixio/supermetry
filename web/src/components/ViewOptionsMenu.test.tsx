import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ViewOptionsMenu } from './ViewOptionsMenu'

afterEach(cleanup)

const openMenu = () => fireEvent.pointerDown(screen.getByRole('button', { name: 'View options' }), { button: 0, ctrlKey: false, pointerType: 'mouse' })

describe('ViewOptionsMenu', () => {
  it('keeps the imported-data option out of sight until the menu opens', () => {
    render(<ViewOptionsMenu includeImported onIncludeImportedChange={vi.fn()} />)
    expect(screen.queryByText('Include imported data')).not.toBeInTheDocument()
    openMenu()
    expect(screen.getByRole('menuitemcheckbox', { name: 'Include imported data' })).toHaveAttribute('aria-checked', 'true')
  })

  it('reports a change of the imported-data option', () => {
    const onChange = vi.fn()
    render(<ViewOptionsMenu includeImported onIncludeImportedChange={onChange} />)
    openMenu()
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: 'Include imported data' }))
    expect(onChange).toHaveBeenCalledWith(false)
  })

  it('shows the option unchecked when imported data is excluded', () => {
    render(<ViewOptionsMenu includeImported={false} onIncludeImportedChange={vi.fn()} />)
    openMenu()
    expect(screen.getByRole('menuitemcheckbox', { name: 'Include imported data' })).toHaveAttribute('aria-checked', 'false')
  })
})
