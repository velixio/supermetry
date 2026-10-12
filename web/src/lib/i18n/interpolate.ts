// A small message formatter: {name} placeholders and one plural form,
// {count, plural, one {# item} other {# items}}, using Intl.PluralRules so every
// language gets its own plural categories. No dependency.

import { activeLocale } from './intl'

export type MessageValues = Record<string, string | number>

const pluralPattern = /\{(\w+),\s*plural,\s*((?:=?\w+\s*\{[^{}]*\}\s*)+)\}/g
const branchPattern = /(=?\w+)\s*\{([^{}]*)\}/g

export function interpolate(template: string, values: MessageValues = {}, locale: string = activeLocale()): string {
  const withPlurals = template.replace(pluralPattern, (_match, name: string, branches: string) => {
    const count = Number(values[name])
    const forms = new Map<string, string>()
    for (const branch of branches.matchAll(branchPattern)) forms.set(branch[1]!, branch[2]!)
    const exact = forms.get(`=${count}`)
    const category = new Intl.PluralRules(locale).select(count)
    const chosen = exact ?? forms.get(category) ?? forms.get('other') ?? ''
    return chosen.replaceAll('#', new Intl.NumberFormat(locale).format(count))
  })
  return withPlurals.replace(/\{(\w+)\}/g, (match, name: string) => (name in values ? String(values[name]) : match))
}

/** Placeholder names a template uses, including plural variables, for the catalogue test. */
export function placeholders(template: string): string[] {
  const names = new Set<string>()
  for (const match of template.matchAll(/\{(\w+)(?:,\s*plural,)?/g)) names.add(match[1]!)
  return [...names].sort()
}
