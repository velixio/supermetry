import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { isApplePlatform, useShortcuts } from './shortcuts'

function ShortcutProbe({ onM }: { onM: () => void }) {
  useShortcuts({ m: onM })
  return <input aria-label="Filter" />
}

describe('useShortcuts', () => {
  afterEach(cleanup)

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

  it('fires mod+ chords for Ctrl or Cmd, also while typing, but never with Alt or for plain-key entries', () => {
    const onK = vi.fn()
    const onM = vi.fn()
    function ChordProbe() {
      useShortcuts({ 'mod+k': onK, m: onM })
      return <input aria-label="Filter" />
    }
    render(<ChordProbe />)
    const input = screen.getByRole('textbox')

    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
    fireEvent.keyDown(document, { key: 'K', metaKey: true })
    input.focus()
    fireEvent.keyDown(input, { key: 'k', ctrlKey: true })
    expect(onK).toHaveBeenCalledTimes(3)

    fireEvent.keyDown(document, { key: 'k' })
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true, altKey: true })
    fireEvent.keyDown(document, { key: 'm', ctrlKey: true })
    expect(onK).toHaveBeenCalledTimes(3)
    expect(onM).not.toHaveBeenCalled()
  })

  it('does not match a mod chord while Shift is held unless the chord says shift', () => {
    const onK = vi.fn()
    const onShiftK = vi.fn()
    function ChordProbe() {
      useShortcuts({ 'mod+k': onK, 'mod+shift+k': onShiftK })
      return null
    }
    render(<ChordProbe />)
    fireEvent.keyDown(document, { key: 'K', ctrlKey: true, shiftKey: true })
    expect(onK).not.toHaveBeenCalled()
    expect(onShiftK).toHaveBeenCalledOnce()
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
    expect(onK).toHaveBeenCalledOnce()
  })

  it('does not prevent the browser default for an unregistered Shift chord', () => {
    function ChordProbe() {
      useShortcuts({ 'mod+k': () => undefined })
      return null
    }
    render(<ChordProbe />)
    const event = new KeyboardEvent('keydown', { key: 'K', ctrlKey: true, shiftKey: true, cancelable: true, bubbles: true })
    document.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(false)
  })

  it('prevents the browser default only for a registered chord', () => {
    function ChordProbe() {
      useShortcuts({ 'mod+k': () => undefined })
      return null
    }
    render(<ChordProbe />)
    const handled = new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, cancelable: true, bubbles: true })
    document.dispatchEvent(handled)
    expect(handled.defaultPrevented).toBe(true)
    const other = new KeyboardEvent('keydown', { key: 'p', ctrlKey: true, cancelable: true, bubbles: true })
    document.dispatchEvent(other)
    expect(other.defaultPrevented).toBe(false)
  })
})

describe('isApplePlatform', () => {
  it('detects Apple platforms for the ⌘ hint and falls back to Ctrl elsewhere', () => {
    expect(isApplePlatform({ platform: 'MacIntel', userAgent: '' })).toBe(true)
    expect(isApplePlatform({ platform: '', userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)' })).toBe(true)
    expect(isApplePlatform({ platform: 'Win32', userAgent: 'Mozilla/5.0 (Windows NT 10.0)' })).toBe(false)
    expect(isApplePlatform({ platform: 'Linux x86_64', userAgent: '' })).toBe(false)
    expect(isApplePlatform(undefined)).toBe(false)
  })

  it('does not throw when navigator is missing', () => {
    vi.stubGlobal('navigator', undefined)
    try {
      expect(isApplePlatform()).toBe(false)
    } finally {
      vi.unstubAllGlobals()
    }
  })
})
