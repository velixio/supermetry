// WCAG contrast helpers for the design tokens in index.css (oklch, optional alpha).
export type Rgba = { r: number; g: number; b: number; a: number }

const clamp01 = (v: number) => Math.min(1, Math.max(0, v))

function srgbEncode(c: number): number {
  const v = clamp01(c)
  return v <= 0.0031308 ? 12.92 * v : 1.055 * Math.pow(v, 1 / 2.4) - 0.055
}

export function oklchToRgb(l: number, c: number, hDeg: number, a = 1): Rgba {
  const h = (hDeg * Math.PI) / 180
  const A = c * Math.cos(h)
  const B = c * Math.sin(h)
  const l_ = Math.pow(l + 0.3963377774 * A + 0.2158037573 * B, 3)
  const m_ = Math.pow(l - 0.1055613458 * A - 0.0638541728 * B, 3)
  const s_ = Math.pow(l - 0.0894841775 * A - 1.291485548 * B, 3)
  return {
    r: srgbEncode(4.0767416621 * l_ - 3.3077115913 * m_ + 0.2309699292 * s_),
    g: srgbEncode(-1.2684380046 * l_ + 2.6097574011 * m_ - 0.3413193965 * s_),
    b: srgbEncode(-0.0041960863 * l_ - 0.7034186147 * m_ + 1.707614701 * s_),
    a,
  }
}

/** Parses `oklch(L C H [/ A])` where L may be a percentage and A may be a percentage. */
export function parseOklch(value: string): Rgba {
  const m = value.trim().match(/^oklch\(\s*([\d.]+%?)\s+([\d.]+)\s+([\d.]+)(?:\s*\/\s*([\d.]+%?))?\s*\)$/)
  if (!m) throw new Error(`unsupported colour: ${value}`)
  const num = (s: string) => (s.endsWith('%') ? parseFloat(s) / 100 : parseFloat(s))
  return oklchToRgb(num(m[1]), parseFloat(m[2]), parseFloat(m[3]), m[4] ? num(m[4]) : 1)
}

/** Composites a possibly translucent colour over an opaque backdrop. */
export function over(fg: Rgba, bg: Rgba): Rgba {
  return {
    r: fg.r * fg.a + bg.r * (1 - fg.a),
    g: fg.g * fg.a + bg.g * (1 - fg.a),
    b: fg.b * fg.a + bg.b * (1 - fg.a),
    a: 1,
  }
}

function lin(c: number): number {
  return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)
}

export function luminance(c: Rgba): number {
  return 0.2126 * lin(c.r) + 0.7152 * lin(c.g) + 0.0722 * lin(c.b)
}

export function contrastRatio(fg: Rgba, bg: Rgba): number {
  const f = over(fg, bg)
  const a = luminance(f)
  const b = luminance(bg)
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)
}
