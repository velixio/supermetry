import { placeholders } from './interpolate'

export type MessageTree = { [key: string]: string | ((...args: never[]) => string) | MessageTree }

const isTree = (value: unknown): value is MessageTree => typeof value === 'object' && value !== null

/**
 * Overlays translated messages onto the live English catalogue in place. Only
 * keys that exist in English are applied, and only with the same kind of value, so
 * a translation can never add, remove or retype a message; anything it omits keeps
 * the English text.
 */
export function mergeMessages(target: MessageTree, source: MessageTree): void {
  for (const [key, value] of Object.entries(source)) {
    const existing = target[key]
    if (existing === undefined) continue
    if (isTree(existing) && isTree(value)) mergeMessages(existing, value)
    else if (typeof existing === typeof value) target[key] = value
  }
}

/** A deep copy that keeps functions, used to restore English after a test. */
export function cloneMessages<T extends MessageTree>(tree: T): T {
  const copy: MessageTree = {}
  for (const [key, value] of Object.entries(tree)) copy[key] = isTree(value) ? cloneMessages(value) : value
  return copy as T
}

export function restoreMessages(target: MessageTree, original: MessageTree): void {
  for (const [key, value] of Object.entries(original)) {
    const existing = target[key]
    if (isTree(value) && isTree(existing)) restoreMessages(existing, value)
    else target[key] = value
  }
}

export type CatalogueProblem = { path: string; problem: 'missing' | 'extra' | 'type' | 'placeholders' | 'arity'; detail?: string }

/** Compares a locale with English: every key present, nothing extra, same placeholders and function arity. */
export function compareCatalogue(base: MessageTree, locale: MessageTree, path = ''): CatalogueProblem[] {
  const problems: CatalogueProblem[] = []
  for (const [key, baseValue] of Object.entries(base)) {
    const here = path ? `${path}.${key}` : key
    const value = locale[key]
    if (value === undefined) {
      problems.push({ path: here, problem: 'missing' })
    } else if (isTree(baseValue)) {
      if (isTree(value)) problems.push(...compareCatalogue(baseValue, value, here))
      else problems.push({ path: here, problem: 'type' })
    } else if (typeof baseValue !== typeof value) {
      problems.push({ path: here, problem: 'type' })
    } else if (typeof baseValue === 'string' && typeof value === 'string') {
      const a = placeholders(baseValue).join(',')
      const b = placeholders(value).join(',')
      if (a !== b) problems.push({ path: here, problem: 'placeholders', detail: `${a} vs ${b}` })
    } else if (typeof baseValue === 'function' && typeof value === 'function' && baseValue.length !== value.length) {
      problems.push({ path: here, problem: 'arity', detail: `${baseValue.length} vs ${value.length}` })
    }
  }
  for (const key of Object.keys(locale)) {
    if (!(key in base)) problems.push({ path: path ? `${path}.${key}` : key, problem: 'extra' })
  }
  return problems
}
