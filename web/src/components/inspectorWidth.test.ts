import { afterEach, describe, expect, it, vi } from 'vitest'

import { INSPECTOR_WIDTH, clampInspectorWidth, readInspectorWidth, remFromPixels, writeInspectorWidth } from './inspectorWidth'

afterEach(() => {
  vi.restoreAllMocks()
  window.localStorage.clear()
  document.documentElement.style.fontSize = ''
})

describe('inspector width', () => {
  it('starts at 24rem and clamps to 20-32rem in quarter steps', () => {
    expect(readInspectorWidth()).toBe(24)
    expect(clampInspectorWidth(10)).toBe(20)
    expect(clampInspectorWidth(40)).toBe(32)
    expect(clampInspectorWidth(25.13)).toBe(25.25)
    expect(clampInspectorWidth(Number.NaN)).toBe(INSPECTOR_WIDTH.initial)
  })

  it('remembers a width and ignores garbage', () => {
    writeInspectorWidth(28)
    expect(window.localStorage.getItem('supermetry:inspector-width')).toBe('28')
    expect(readInspectorWidth()).toBe(28)
    window.localStorage.setItem('supermetry:inspector-width', 'wide')
    expect(readInspectorWidth()).toBe(24)
    window.localStorage.setItem('supermetry:inspector-width', ' ')
    expect(readInspectorWidth()).toBe(24)
    window.localStorage.setItem('supermetry:inspector-width', '99')
    expect(readInspectorWidth()).toBe(32)
  })

  it('works when the browser blocks storage', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked') })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked') })
    expect(readInspectorWidth()).toBe(24)
    expect(() => writeInspectorWidth(30)).not.toThrow()
  })

  it('converts pixels using the root font size', () => {
    document.documentElement.style.fontSize = '20px'
    expect(remFromPixels(480)).toBe(24)
  })
})
