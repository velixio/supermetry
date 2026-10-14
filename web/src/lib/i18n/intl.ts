// The active interface language, resolved synchronously at import so that
// formatters created at module scope already use it. The message catalogue itself
// loads lazily (see ./index.ts).

export type LocaleCode = 'en' | 'es' | 'de' | 'bn' | 'en-XA'

export const storageKey = 'supermetry.locale'
export const supportedLocales: readonly LocaleCode[] = ['en', 'es', 'de', 'bn', 'en-XA']

export function isLocaleCode(value: unknown): value is LocaleCode {
  return typeof value === 'string' && (supportedLocales as readonly string[]).includes(value)
}

function readStored(): LocaleCode | undefined {
  try {
    const value = window.localStorage.getItem(storageKey)
    return isLocaleCode(value) ? value : undefined
  } catch {
    return undefined
  }
}

/** Picks the first browser language we ship; the pseudo-locale is never auto-selected. */
export function detectBrowserLocale(languages: readonly string[] = typeof navigator === 'undefined' ? [] : navigator.languages): LocaleCode {
  for (const tag of languages) {
    const base = tag.toLowerCase().split('-')[0]
    if (base === 'es' || base === 'de' || base === 'bn' || base === 'en') return base
  }
  return 'en'
}

let current: LocaleCode = typeof window === 'undefined' ? 'en' : readStored() ?? detectBrowserLocale()

export function activeLocaleCode(): LocaleCode {
  return current
}

export function setActiveLocaleCode(code: LocaleCode): void {
  current = code
}

/** BCP 47 tag handed to Intl. The pseudo-locale formats like English. */
export function activeLocale(): string {
  // Analytics numbers stay in ASCII 0-9 in Bengali charts, tables and tests.
  return current === 'en-XA' ? 'en' : current === 'en' ? 'en-US' : current === 'bn' ? 'bn-u-nu-latn' : current
}

type WeekInfo = { firstDay?: number }

/** First day of the week, 0 = Sunday ... 6 = Saturday, from the locale's own rules. */
export function weekStart(locale: string = activeLocale()): number {
  const intlLocale = new Intl.Locale(locale) as Intl.Locale & { getWeekInfo?: () => WeekInfo; weekInfo?: WeekInfo }
  const info = intlLocale.getWeekInfo?.() ?? intlLocale.weekInfo
  if (info?.firstDay !== undefined) return info.firstDay % 7
  // Fallback: Sunday for the US, Monday for the languages we ship otherwise.
  return locale.startsWith('en-US') || locale === 'en' ? 0 : 1
}

export function formatRelativeTime(value: number, unit: Intl.RelativeTimeFormatUnit, numeric: 'always' | 'auto' = 'always'): string {
  return new Intl.RelativeTimeFormat(activeLocale(), { numeric }).format(value, unit)
}

export function formatNumber(value: number, options?: Intl.NumberFormatOptions): string {
  return new Intl.NumberFormat(activeLocale(), options).format(value)
}

export function formatCurrency(value: number, currency: string, options?: Intl.NumberFormatOptions): string {
  return new Intl.NumberFormat(activeLocale(), { style: 'currency', currency, ...options }).format(value)
}

export function formatDateTime(value: Date | number | string, options?: Intl.DateTimeFormatOptions): string {
  return new Intl.DateTimeFormat(activeLocale(), options).format(new Date(value))
}
