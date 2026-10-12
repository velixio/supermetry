import type { MessageTree } from './catalogue'

const accents: Record<string, string> = {
  a: 'á', b: 'ƀ', c: 'ç', d: 'ď', e: 'é', f: 'ƒ', g: 'ğ', h: 'ħ', i: 'í', j: 'ĵ', k: 'ķ', l: 'ł', m: 'ɱ',
  n: 'ñ', o: 'ö', p: 'ƥ', q: 'ɋ', r: 'ŕ', s: 'š', t: 'ţ', u: 'ü', v: 'ṽ', w: 'ŵ', x: 'ẋ', y: 'ý', z: 'ž',
  A: 'Á', B: 'Ɓ', C: 'Ç', D: 'Ď', E: 'É', F: 'Ƒ', G: 'Ğ', H: 'Ħ', I: 'Í', J: 'Ĵ', K: 'Ķ', L: 'Ł', M: 'Ṁ',
  N: 'Ñ', O: 'Ö', P: 'Ƥ', Q: 'Ɋ', R: 'Ŕ', S: 'Š', T: 'Ţ', U: 'Ü', V: 'Ṽ', W: 'Ŵ', X: 'Ẋ', Y: 'Ý', Z: 'Ž',
}

/**
 * Accents every letter and pads the text by about 40 percent so layouts that cannot
 * cope with longer languages fail visibly. Placeholders and plural syntax are kept.
 */
export function pseudoString(text: string): string {
  if (!text.trim()) return text
  let inBrace = 0
  let out = ''
  for (const char of text) {
    if (char === '{') inBrace += 1
    out += inBrace > 0 ? char : accents[char] ?? char
    if (char === '}' && inBrace > 0) inBrace -= 1
  }
  const padding = '·'.repeat(Math.max(1, Math.round(text.length * 0.4)))
  return `[${out}${padding}]`
}

export function pseudoMessages(tree: MessageTree): MessageTree {
  const out: MessageTree = {}
  for (const [key, value] of Object.entries(tree)) {
    if (typeof value === 'string') out[key] = pseudoString(value)
    else if (typeof value === 'function') {
      const original = value as (...args: unknown[]) => string
      out[key] = ((...args: unknown[]) => pseudoString(original(...args))) as never
    } else out[key] = pseudoMessages(value)
  }
  return out
}
