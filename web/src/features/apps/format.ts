export function formatRate(rate: number | null | undefined): string {
  return rate === null || rate === undefined ? 'n/a' : `${(rate * 100).toFixed(1)}%`
}

/** Milliseconds with one decimal, e.g. `97.2 ms`. */
export function formatMs(ms: number | null | undefined): string {
  return ms === null || ms === undefined || !Number.isFinite(ms) ? 'n/a' : `${ms.toFixed(1)} ms`
}

export function formatDay(iso: string): string {
  return iso.slice(0, 10)
}

/** A signed percentage-point change, e.g. -0.03 -> "−3.0 pp"; "n/a" when either side has no rate. */
export function formatDeltaPp(delta: number | null | undefined): string {
  if (delta === null || delta === undefined) return 'n/a'
  const pp = Math.round(delta * 1000) / 10
  if (pp === 0) return '0.0 pp'
  return `${pp < 0 ? '−' : '+'}${Math.abs(pp).toFixed(1)} pp`
}

/** The calendar day (YYYY-MM-DD) of an instant in the site's time zone, matching the chart's day buckets. */
export function dayInZone(iso: string, timezone: string): string {
  try {
    return new Intl.DateTimeFormat('en-CA', { timeZone: timezone || 'UTC', year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(iso))
  } catch {
    return iso.slice(0, 10)
  }
}

/** The link as a safe href, or null: only https URLs on exactly github.com are ever rendered as links. */
export function safeGitHubUrl(value: string | null | undefined): string | null {
  if (!value) return null
  try {
    const url = new URL(value)
    return url.protocol === 'https:' && url.hostname === 'github.com' && !url.username && !url.password ? url.href : null
  } catch {
    return null
  }
}
