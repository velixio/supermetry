import { readFileSync } from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'

import { contrastRatio, over, parseOklch, type Rgba } from './contrast'

const css = readFileSync(path.resolve(__dirname, '../index.css'), 'utf8')

function block(selector: string): Record<string, string> {
  const start = css.indexOf(`\n${selector} {`)
  if (start < 0) throw new Error(`missing ${selector} block`)
  const body = css.slice(css.indexOf('{', start) + 1, css.indexOf('}', start))
  const vars: Record<string, string> = {}
  for (const m of body.matchAll(/--([\w-]+):\s*([^;]+);/g)) vars[m[1]] = m[2].trim()
  return vars
}

const themes = { light: block(':root'), dark: { ...block(':root'), ...block('.dark') } }

type Pair = { fg: string; bg: string; min: number }
const text = (fg: string, bg: string): Pair => ({ fg, bg, min: 4.5 })
const ui = (fg: string, bg: string): Pair => ({ fg, bg, min: 3 })

// Text must reach 4.5:1 and non-text UI (input borders, focus ring, chart lines) 3:1.
const pairs: Pair[] = [
  text('foreground', 'background'),
  text('muted-foreground', 'background'),
  text('muted-foreground', 'card'),
  text('muted-foreground', 'muted'),
  text('muted-foreground', 'secondary'),
  text('muted-foreground', 'sidebar'),
  text('card-foreground', 'card'),
  text('popover-foreground', 'popover'),
  text('muted-foreground', 'popover'),
  text('primary-foreground', 'primary'),
  text('secondary-foreground', 'secondary'),
  text('accent-foreground', 'accent'),
  text('sidebar-foreground', 'sidebar'),
  text('sidebar-accent-foreground', 'sidebar-accent'),
  text('sidebar-primary-foreground', 'sidebar-primary'),
  text('primary', 'background'),
  text('primary', 'card'),
  text('destructive', 'background'),
  text('destructive', 'card'),
  text('destructive-foreground', 'destructive-solid'),
  text('success', 'background'),
  text('success', 'card'),
  text('warning', 'background'),
  text('warning', 'card'),
  ui('input', 'background'),
  ui('input', 'card'),
  ui('ring', 'background'),
  ui('ring', 'card'),
  ui('ring', 'popover'),
  ui('sidebar-ring', 'sidebar'),
  ui('chart-1', 'card'),
  ui('chart-2', 'card'),
  ui('chart-3', 'card'),
  ui('chart-4', 'card'),
  ui('chart-5', 'card'),
  // The selected segment/tab sits on the muted track and must stand out from it.
  ui('selected-edge', 'muted'),
]

describe.each(Object.entries(themes))('%s theme contrast', (_name, vars) => {
  const resolve = (token: string, backdrop?: Rgba): Rgba => {
    const raw = vars[token]
    if (!raw) throw new Error(`token --${token} is not defined`)
    const rgba = parseOklch(raw)
    return backdrop ? over(rgba, backdrop) : rgba
  }
  it.each(pairs)('$fg on $bg is at least $min:1', ({ fg, bg, min }) => {
    const back = resolve(bg)
    const ratio = contrastRatio(resolve(fg, back), back)
    expect(ratio, `--${fg} on --${bg} = ${ratio.toFixed(2)}`).toBeGreaterThanOrEqual(min)
  })
})

describe('contrast helpers', () => {
  it('white on black is 21:1', () => {
    expect(contrastRatio(parseOklch('oklch(1 0 0)'), parseOklch('oklch(0 0 0)'))).toBeCloseTo(21, 0)
  })
})
