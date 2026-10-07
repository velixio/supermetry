import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { useShortcuts } from './shortcuts'

function ShortcutProbe({ onM }: { onM: () => void }) {
  useShortcuts({ m: onM })
  return <input aria-label="Filter" />
}

describe('useShortcuts', () => {
  it('ignores shortcuts while focus is inside an input', () => {
    const onM = vi.fn()
    render(<ShortcutProbe onM={onM} />)
    const input = screen.getByRole('textbox')

    input.focus()
    fireEvent.keyDown(input, { key: 'm' })
    expect(onM).not.toHaveBeenCalled()

    input.blur()
    fireEvent.keyDown(document, { key: 'm' })
    expect(onM).toHaveBeenCalledOnce()
  })
})
