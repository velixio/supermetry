const regularNumber = new Intl.NumberFormat('en-US', {
  maximumFractionDigits: 2,
})

const compactNumber = new Intl.NumberFormat('en-US', {
  notation: 'compact',
  maximumFractionDigits: 1,
})

const percentNumber = new Intl.NumberFormat('en-US', {
  style: 'unit',
  unit: 'percent',
  unitDisplay: 'narrow',
  maximumFractionDigits: 1,
})

function number(value: number): string {
  return Math.abs(value) >= 10_000
    ? compactNumber.format(value)
    : regularNumber.format(value)
}

function percent(value: number): string {
  return percentNumber.format(value)
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
    return new Intl.DateTimeFormat('en-US', {
      timeZone: zone,
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    }).format(instant)
  } catch {
    return new Intl.DateTimeFormat('en-US', {
      timeZone: 'UTC',
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    }).format(instant)
  }
}

export const fmt = { number, percent, duration, date }
