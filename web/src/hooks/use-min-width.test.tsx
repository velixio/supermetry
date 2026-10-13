import { act, cleanup, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { useIsMobile } from './use-mobile'
import { useMinWidth } from './use-min-width'

let handlers: Array<() => void>
let removeEventListener: ReturnType<typeof vi.fn>

function setWidth(width: number) {
  vi.stubGlobal('innerWidth', width)
}

beforeEach(() => {
  handlers = []
  removeEventListener = vi.fn()
  vi.stubGlobal('matchMedia', vi.fn().mockReturnValue({
    matches: false,
    addEventListener: (_: string, handler: () => void) => handlers.push(handler),
    removeEventListener,
  }))
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

it('useMinWidth treats the minimum as inclusive and follows matchMedia changes', () => {
  setWidth(1279)
  const { result, unmount } = renderHook(() => useMinWidth(1280))
  expect(result.current).toBe(false)
  setWidth(1280)
  act(() => handlers.forEach((handler) => handler()))
  expect(result.current).toBe(true)
  unmount()
  expect(removeEventListener).toHaveBeenCalledWith('change', handlers[0])
})

it('useIsMobile is true below 768 only and follows matchMedia changes', () => {
  setWidth(768)
  const { result, unmount } = renderHook(() => useIsMobile())
  expect(result.current).toBe(false)
  setWidth(767)
  act(() => handlers.forEach((handler) => handler()))
  expect(result.current).toBe(true)
  unmount()
  expect(removeEventListener).toHaveBeenCalledWith('change', handlers[0])
})
