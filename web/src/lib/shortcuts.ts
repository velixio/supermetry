import { useEffect, useRef } from 'react'

function isEditing(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  return (
    target instanceof HTMLInputElement ||
    target instanceof HTMLTextAreaElement ||
    target.isContentEditable
  )
}

/**
 * The map key for a keydown: the lower-cased key, `mod+<key>` while Ctrl or Cmd is held, or
 * `mod+shift+<key>` when Shift is held too, so `mod+k` never fires for Shift+Ctrl+K.
 * Alt combinations are never shortcuts. Plain keys ignore Shift (`?` needs it on most layouts).
 */
function shortcutKey(event: KeyboardEvent): string | undefined {
  if (event.altKey) return undefined
  const key = event.key.toLowerCase()
  if (!(event.ctrlKey || event.metaKey)) return key
  return event.shiftKey ? `mod+shift+${key}` : `mod+${key}`
}

/**
 * Document-wide shortcuts. Plain keys (`d`, `?`) are ignored while the user types in a field.
 * `mod+` chords (Ctrl or Cmd, e.g. `mod+k`) also fire from inside a field.
 */
export function useShortcuts(map: Record<string, () => void>) {
  const mapRef = useRef(map)
  mapRef.current = map

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      const key = shortcutKey(event)
      if (!key) return
      if (!key.startsWith('mod+') && isEditing(event.target)) return
      const action = mapRef.current[key]
      if (!action) return
      event.preventDefault()
      action()
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [])
}

/** Whether to show ⌘ rather than Ctrl in shortcut hints. */
export function isApplePlatform(
  nav: Pick<Navigator, 'platform' | 'userAgent'> | undefined = typeof navigator === 'undefined' ? undefined : navigator,
): boolean {
  if (!nav) return false
  return /Mac|iPhone|iPad|iPod/i.test(nav.platform || nav.userAgent)
}

/** How long the second key of a `g` chord may take. */
export const GO_TO_WINDOW_MS = 1000

function typingTarget(target: EventTarget | null): boolean {
  return target instanceof HTMLElement && (target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName))
}

/**
 * "Go to" chords: press `g`, then a letter within a second (`g d`). Ignored while typing in a field,
 * while a dialog is open, and with Ctrl, Cmd or Alt held. Any other second key cancels the chord.
 * The map's keys are the lower-cased second key.
 */
export function useGoTo(map: Record<string, () => void>) {
  const mapRef = useRef(map)
  mapRef.current = map

  useEffect(() => {
    let armedAt = 0
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.ctrlKey || event.metaKey || event.altKey || event.key === 'Shift') return
      if (typingTarget(event.target) || document.querySelector('[role="dialog"]')) {
        armedAt = 0
        return
      }
      const key = event.key.toLowerCase()
      if (armedAt && Date.now() - armedAt <= GO_TO_WINDOW_MS) {
        armedAt = 0
        const action = mapRef.current[key]
        if (action) {
          event.preventDefault()
          action()
        }
        return
      }
      armedAt = key === 'g' && !event.shiftKey ? Date.now() : 0
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [])
}
