import { useEffect, useRef } from 'react'

function isEditing(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  return (
    target instanceof HTMLInputElement ||
    target instanceof HTMLTextAreaElement ||
    target.isContentEditable
  )
}

/** The map key for a keydown: the lower-cased key, or `mod+<key>` while Ctrl or Cmd is held. Alt combinations are never shortcuts. */
function shortcutKey(event: KeyboardEvent): string | undefined {
  if (event.altKey) return undefined
  const key = event.key.toLowerCase()
  return event.ctrlKey || event.metaKey ? `mod+${key}` : key
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
