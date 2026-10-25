import { AnnotationManager } from '@/features/annotations/AnnotationManager'
import type { ChartInterval } from '@/lib/chart'
import type { Compare, DashboardMetric } from '@/lib/search'
import type { StatsQuery } from '@/lib/stats'
import { MainChart } from './MainChart'

export function DashboardChart({
  anonymous,
  canEdit,
  compare,
  metric,
  query,
  siteId,
  interval,
  onIntervalChange,
}: {
  anonymous: boolean
  canEdit: boolean
  compare: Compare
  metric: DashboardMetric
  query: StatsQuery
  siteId: number
  interval?: ChartInterval
  onIntervalChange?: (interval: ChartInterval) => void
}) {
  if (anonymous) return <MainChart query={query} metric={metric} compare={compare} interval={interval} onIntervalChange={onIntervalChange} />
  return (
    <AnnotationManager siteId={siteId} dateRange={query.dateRange} canEdit={canEdit}>
      {({ addButton, annotations, onEdit, onDelete }) => (
        <MainChart
          query={query}
          metric={metric}
          compare={compare}
          interval={interval}
          onIntervalChange={onIntervalChange}
          headerActions={addButton}
          annotations={annotations}
          canEditAnnotations={canEdit}
          onEditAnnotation={onEdit}
          onDeleteAnnotation={onDelete}
        />
      )}
    </AnnotationManager>
  )
}
