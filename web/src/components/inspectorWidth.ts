/** Inspector widths in rem: spec section 11.1 (default 24, resizable 20-32). */
export const INSPECTOR_WIDTH = { min: 20, max: 32, initial: 24 } as const

const storageKey = 'supermetry:inspector-width'

export function clampInspectorWidth(rem: number): number {
  if (!Number.isFinite(rem)) return INSPECTOR_WIDTH.initial
  return Math.min(INSPECTOR_WIDTH.max, Math.max(INSPECTOR_WIDTH.min, Math.round(rem * 4) / 4))
}

export function readInspectorWidth(): number {
  try {
    const raw = window.localStorage.getItem(storageKey)
    return raw === null || raw.trim() === '' ? INSPECTOR_WIDTH.initial : clampInspectorWidth(Number(raw))
  } catch {
    return INSPECTOR_WIDTH.initial
  }
}

export function writeInspectorWidth(rem: number): void {
  try {
    window.localStorage.setItem(storageKey, String(clampInspectorWidth(rem)))
  } catch {
    // Storage blocked or full: the width simply resets on the next visit.
  }
}

export function remFromPixels(pixels: number): number {
  const root = Number.parseFloat(getComputedStyle(document.documentElement).fontSize)
  return pixels / (Number.isFinite(root) && root > 0 ? root : 16)
}
