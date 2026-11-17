export function formatRate(rate: number | null | undefined): string {
  return rate === null || rate === undefined ? 'n/a' : `${(rate * 100).toFixed(1)}%`
}

export function formatDay(iso: string): string {
  return iso.slice(0, 10)
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
