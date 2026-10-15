export type ChartInterval = 'hour' | 'day' | 'week' | 'month'

export const maxHourlyRangeDays = 30

export function autoInterval(from: string, to: string): ChartInterval {
  const days = Math.floor((new Date(`${to}T00:00:00Z`).getTime() - new Date(`${from}T00:00:00Z`).getTime()) / 86_400_000) + 1
  if (days <= 2) return 'hour'
  if (days <= 92) return 'day'
  if (days <= 183) return 'week'
  return 'month'
}
