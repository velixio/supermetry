import type { InspectControl } from '@/lib/inspect'
import { useBreakdown, type StatsQuery } from '@/lib/stats'

import { TrafficGrid } from './TrafficGrid'
import type { TrafficMetric } from './trafficGrid'

export function WeeklyRhythmCard({ query, metric, onShorten, inspect }: {
  query: StatsQuery
  metric: TrafficMetric
  onShorten(days: number): void
  inspect?: InspectControl
}) {
  const traffic = useBreakdown({ ...query, metrics: [metric], dimensions: ['weekday', 'hour_of_day'], limit: 168 })
  return (
    <TrafficGrid
      dateRange={query.dateRange}
      error={traffic.error}
      inspect={inspect}
      isLoading={traffic.isPending}
      metric={metric}
      onRetry={() => void traffic.refetch()}
      onShorten={onShorten}
      rows={traffic.data?.rows ?? []}
      timezone={query.timezone ?? 'UTC'}
    />
  )
}
