import '@testing-library/jest-dom/vitest'

import { act, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'

import { useTheme } from './theme'

function ThemeProbe() {
  const { theme, toggle } = useTheme()
  return <button onClick={toggle}>{theme}</button>
}

describe('useTheme', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.classList.remove('dark')
  })

  it('persists a theme toggle and updates the document class', () => {
    render(<ThemeProbe />)

    act(() => screen.getByRole('button').click())

    expect(screen.getByRole('button')).toHaveTextContent('dark')
    expect(document.documentElement).toHaveClass('dark')
    expect(localStorage.getItem('theme')).toBe('dark')
  })
})
