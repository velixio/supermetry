export function formatRate(rate: number | null | undefined): string {
  return rate === null || rate === undefined ? 'n/a' : `${(rate * 100).toFixed(1)}%`
}

export function formatDay(iso: string): string {
  return iso.slice(0, 10)
}
