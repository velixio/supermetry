// Next occurrences of a weekly maintenance window, computed in its time zone
// the way the server does: the wall-clock start on each matching local date.

/** The offset (ms) of a zone from UTC at an instant. */
function zoneOffset(at: number, timeZone: string): number {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone, hourCycle: 'h23', year: 'numeric', month: 'numeric', day: 'numeric', hour: 'numeric', minute: 'numeric', second: 'numeric',
  }).formatToParts(new Date(at))
  const get = (type: string) => Number(parts.find((p) => p.type === type)?.value)
  return Date.UTC(get('year'), get('month') - 1, get('day'), get('hour'), get('minute'), get('second')) - Math.floor(at / 1000) * 1000
}

/** The instant of a wall-clock time in a zone. */
export function zonedInstant(year: number, month: number, day: number, hour: number, minute: number, timeZone: string): number {
  const guess = Date.UTC(year, month - 1, day, hour, minute)
  const first = guess - zoneOffset(guess, timeZone)
  return guess - zoneOffset(first, timeZone)
}

export type Occurrence = { start: Date; end: Date }

/**
 * The next `count` windows that start after `from`. weekdays use 0 for Sunday;
 * startTime is "HH:MM"; the search looks 15 days ahead.
 */
export function nextOccurrences(weekdays: number[], startTime: string, durationMinutes: number, timeZone: string, from: Date, count = 2): Occurrence[] {
  const [hour, minute] = startTime.split(':').map(Number)
  if (!weekdays.length || Number.isNaN(hour) || Number.isNaN(minute) || !(durationMinutes > 0)) return []
  const out: Occurrence[] = []
  const today = new Intl.DateTimeFormat('en-US', { timeZone, year: 'numeric', month: 'numeric', day: 'numeric' }).formatToParts(from)
  const num = (type: string) => Number(today.find((p) => p.type === type)?.value)
  for (let offset = 0; offset < 15 && out.length < count; offset++) {
    const date = new Date(Date.UTC(num('year'), num('month') - 1, num('day') + offset))
    if (!weekdays.includes(date.getUTCDay())) continue
    const start = zonedInstant(date.getUTCFullYear(), date.getUTCMonth() + 1, date.getUTCDate(), hour, minute, timeZone)
    if (start > from.getTime()) out.push({ start: new Date(start), end: new Date(start + durationMinutes * 60_000) })
  }
  return out
}

export function formatInZone(date: Date, timeZone: string): string {
  return new Intl.DateTimeFormat('en-GB', { timeZone, weekday: 'short', day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(date)
}
