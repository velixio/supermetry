import { afterEach, describe, expect, it } from 'vitest'

import { cloneMessages, compareCatalogue, mergeMessages, restoreMessages, type MessageTree } from './catalogue'
import { initI18n, registerCatalogue, switchLocale } from './index'
import { interpolate, placeholders } from './interpolate'
import { activeLocale, detectBrowserLocale, formatCurrency, formatNumber, formatRelativeTime, setActiveLocaleCode, weekStart } from './intl'
import { pseudoMessages, pseudoString } from './pseudo'

describe('interpolate', () => {
  it('fills named placeholders and leaves unknown ones visible', () => {
    expect(interpolate('Hello {name}, {missing}', { name: 'Ana' }, 'en')).toBe('Hello Ana, {missing}')
  })

  it('picks plural forms per language, including exact matches and #', () => {
    const template = '{count, plural, =0 {No items} one {# item} other {# items}}'
    expect(interpolate(template, { count: 0 }, 'en')).toBe('No items')
    expect(interpolate(template, { count: 1 }, 'en')).toBe('1 item')
    expect(interpolate(template, { count: 1200 }, 'en')).toBe('1,200 items')
    expect(interpolate(template, { count: 1200 }, 'de')).toBe('1.200 items')
  })

  it('uses a language with more plural categories', () => {
    const template = '{n, plural, one {# день} few {# дня} many {# дней} other {# дня}}'
    expect(interpolate(template, { n: 2 }, 'ru')).toBe('2 дня')
    expect(interpolate(template, { n: 5 }, 'ru')).toBe('5 дней')
    expect(interpolate(template, { n: 21 }, 'ru')).toBe('21 день')
  })

  it('lists the placeholders a template uses', () => {
    expect(placeholders('Hi {name}, {count, plural, one {# a} other {# b}}')).toEqual(['count', 'name'])
  })
})

describe('catalogue merging', () => {
  const english = (): MessageTree => ({ title: 'Title', nested: { a: 'A', fn: (n: number) => `n=${n}` }, only: 'English only' })

  it('overlays matching keys and keeps English for omitted ones', () => {
    const live = english()
    mergeMessages(live, { title: 'Titel', nested: { a: 'Ä' } })
    expect(live).toMatchObject({ title: 'Titel', nested: { a: 'Ä' }, only: 'English only' })
    expect(typeof (live.nested as MessageTree).fn).toBe('function')
  })

  it('ignores keys English does not have and values of another type', () => {
    const live = english()
    mergeMessages(live, { extra: 'x', title: (() => 'no') as never })
    expect(live.extra).toBeUndefined()
    expect(live.title).toBe('Title')
  })

  it('restores English after an overlay', () => {
    const live = english()
    const original = cloneMessages(live)
    mergeMessages(live, { title: 'Titel' })
    restoreMessages(live, original)
    expect(live.title).toBe('Title')
  })

  it('reports missing, extra, retyped, placeholder and arity problems', () => {
    const base = { a: 'Hi {name}', b: 'B', c: (x: number, y: number) => `${x}${y}`, d: { e: 'E' } } as unknown as MessageTree
    const bad = { a: 'Hallo', c: (x: number) => `${x}`, d: 'flat', z: 'extra' } as unknown as MessageTree
    const problems = compareCatalogue(base, bad).map((p) => `${p.path}:${p.problem}`).sort()
    expect(problems).toEqual(['a:placeholders', 'b:missing', 'c:arity', 'd:type', 'z:extra'])
    expect(compareCatalogue(base, base)).toEqual([])
  })
})

describe('pseudo-locale', () => {
  it('accents letters, pads by about 40 percent and keeps placeholders intact', () => {
    const text = 'Save {name} now'
    const out = pseudoString(text)
    expect(out.startsWith('[') && out.endsWith(']')).toBe(true)
    expect(out).toContain('{name}')
    expect(out.length).toBeGreaterThanOrEqual(Math.round(text.length * 1.4))
    expect(out).not.toContain('Save')
    expect(placeholders(out)).toEqual(['name'])
  })

  it('transforms nested strings and function results', () => {
    const out = pseudoMessages({ a: 'Hello', n: { f: (count: number) => `${count} items` } } as MessageTree)
    expect(out.a).toContain('é')
    expect(((out.n as MessageTree).f as (c: number) => string)(3)).toMatch(/^\[3 /)
  })
})

describe('live catalogue switching', () => {
  afterEach(async () => {
    await switchLocale('en')
  })

  it('applies the pseudo-locale in place and restores English', async () => {
    const tree = { greeting: 'Welcome', deep: { label: 'Open' } }
    registerCatalogue('i18n-test', tree)
    await switchLocale('en-XA')
    expect(tree.greeting).not.toBe('Welcome')
    expect(tree.deep.label).toMatch(/^\[/)
    await switchLocale('en')
    expect(tree).toEqual({ greeting: 'Welcome', deep: { label: 'Open' } })
    expect(document.documentElement.lang).toBe('en')
    await initI18n()
  })
})

describe('locale helpers', () => {
  afterEach(() => setActiveLocaleCode('en'))

  it('detects the first shipped browser language', () => {
    expect(detectBrowserLocale(['fr-FR', 'de-AT', 'en'])).toBe('de')
    expect(detectBrowserLocale(['ja'])).toBe('en')
  })

  it('formats numbers, currency and relative time in the active language', () => {
    setActiveLocaleCode('de')
    expect(activeLocale()).toBe('de')
    expect(formatNumber(1234.5)).toBe('1.234,5')
    expect(formatCurrency(1234.5, 'EUR')).toMatch(/1\.234,50\s€/)
    expect(formatRelativeTime(-2, 'day')).toBe('vor 2 Tagen')
    setActiveLocaleCode('en')
    expect(formatNumber(1234.5)).toBe('1,234.5')
  })

  it('knows the first day of the week per locale', () => {
    expect(weekStart('en-US')).toBe(0)
    expect(weekStart('de')).toBe(1)
    expect(weekStart('es')).toBe(1)
  })
})
