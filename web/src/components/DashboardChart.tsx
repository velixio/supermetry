import { AnnotationManager } from '@/features/annotations/AnnotationManager'
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
}: {
  anonymous: boolean
  canEdit: boolean
  compare: Compare
  metric: DashboardMetric
  query: StatsQuery
  siteId: number
}) {
  if (anonymous) return <MainChart query={query} metric={metric} compare={compare} />
  return (
    <AnnotationManager siteId={siteId} dateRange={query.dateRange} canEdit={canEdit}>
      {({ annotations, onEdit, onDelete }) => (
        <MainChart
          query={query}
          metric={metric}
          compare={compare}
          annotations={annotations}
          canEditAnnotations={canEdit}
          onEditAnnotation={onEdit}
          onDeleteAnnotation={onDelete}
        />
      )}
    </AnnotationManager>
  )
}
