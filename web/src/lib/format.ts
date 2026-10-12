import { activeLocale } from '@/lib/i18n/intl'

// Formatters are cached per language so a locale change never reuses another one's.
const cache = new Map<string, Intl.NumberFormat>()
function numberFormat(kind: 'regular' | 'compact' | 'percent'): Intl.NumberFormat {
  const key = `${activeLocale()}:${kind}`
  let format = cache.get(key)
  if (!format) {
    const locale = activeLocale()
    format = kind === 'regular'
      ? new Intl.NumberFormat(locale, { maximumFractionDigits: 2 })
      : kind === 'compact'
        ? new Intl.NumberFormat(locale, { notation: 'compact', maximumFractionDigits: 1 })
        : new Intl.NumberFormat(locale, { style: 'unit', unit: 'percent', unitDisplay: 'narrow', maximumFractionDigits: 1 })
    cache.set(key, format)
  }
  return format
}

function number(value: number): string {
  return Math.abs(value) >= 10_000
    ? numberFormat('compact').format(value)
    : numberFormat('regular').format(value)
}

function percent(value: number): string {
  return numberFormat('percent').format(value)
}

function duration(value: number): string {
  let remaining = Math.max(0, Math.round(value))
  const hours = Math.floor(remaining / 3_600)
  remaining %= 3_600
  const minutes = Math.floor(remaining / 60)
  const seconds = remaining % 60
  const parts: string[] = []
  if (hours > 0) parts.push(`${hours}h`)
  if (minutes > 0) parts.push(`${minutes}m`)
  if (seconds > 0 || parts.length === 0) {
    const displaySeconds = hours === 0 && minutes > 0
      ? seconds.toString().padStart(2, '0')
      : seconds.toString()
    parts.push(`${displaySeconds}s`)
  }
  return parts.join(' ')
}

function date(value: string, timeZone: string): string {
  const instant = /^\d{4}-\d{2}-\d{2}$/.test(value)
    ? new Date(`${value}T12:00:00Z`)
    : new Date(value)
  const zone = /^\d{4}-\d{2}-\d{2}$/.test(value) ? 'UTC' : timeZone
  try {
    return new Intl.DateTimeFormat(activeLocale(), {
      timeZone: zone,
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    }).format(instant)
  } catch {
    return new Intl.DateTimeFormat(activeLocale(), {
      timeZone: 'UTC',
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    }).format(instant)
  }
}

export const fmt = { number, percent, duration, date }
