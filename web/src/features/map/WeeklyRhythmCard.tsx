import { useBreakdown, type StatsQuery } from '@/lib/stats'

import { TrafficGrid } from './TrafficGrid'
import type { TrafficMetric } from './trafficGrid'

export function WeeklyRhythmCard({ query, metric, onShorten }: {
  query: StatsQuery
  metric: TrafficMetric
  onShorten(days: number): void
}) {
  const traffic = useBreakdown({ ...query, metrics: [metric], dimensions: ['weekday', 'hour_of_day'], limit: 168 })
  return (
    <TrafficGrid
      dateRange={query.dateRange}
      error={traffic.error}
      isLoading={traffic.isPending}
      metric={metric}
      onRetry={() => void traffic.refetch()}
      onShorten={onShorten}
      rows={traffic.data?.rows ?? []}
      timezone={query.timezone ?? 'UTC'}
    />
  )
}
