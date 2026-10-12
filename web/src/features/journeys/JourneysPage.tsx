import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { EmptyState } from '@/components/EmptyState'
import { ErrorState } from '@/components/ErrorState'
import { Skeleton } from '@/components/ui/skeleton'
import type { Filter } from '@/lib/search'
import { useStats } from '@/lib/stats'

import { useJourneyReport } from './api'
import { JourneyControls } from './JourneyControls'
import { JourneyFlow } from './JourneyFlow'
import type { JourneyDirection } from './types'

type JourneysPageProps = {
  siteId: number
  dateRange: { from: string; to: string }
  filters: Filter[]
  direction: JourneyDirection
  path: string
  steps: number
  onControlsChange: (value: { direction: JourneyDirection; path: string; steps: number }) => void
}

export function JourneysPage({
  siteId,
  dateRange,
  filters,
  direction,
  path,
  steps,
  onControlsChange,
}: JourneysPageProps) {
  const pages = useStats({
    siteId,
    dateRange,
    filters,
    metrics: ['visitors'],
    dimensions: ['page'],
    orderBy: [{ column: 'visitors', direction: 'desc' }],
    limit: 10,
  })
  const report = useJourneyReport(siteId, dateRange, filters, direction, path, steps)
  const topPages = (pages.data?.rows ?? [])
    .map((row) => String(row.page ?? ''))
    .filter((page) => page !== '')

  return (
    <PageContainer>
      <PageHeader title="User journeys" description="Follow the pages visitors viewed in sequence. Repeated consecutive pageviews are collapsed." />
      <JourneyControls
        direction={direction}
        path={path}
        steps={steps}
        topPages={topPages}
        onChange={onControlsChange}
      />
      {report.isError ? (
        <ErrorState detail={report.error?.detail} onRetry={() => void report.refetch()} />
      ) : report.isPending || report.isLoading ? (
        <div aria-label="Loading journeys" className="flex flex-col gap-3 rounded-xl border p-4">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-4/5" />
          <Skeleton className="h-16 w-3/5" />
        </div>
      ) : !report.data || report.data.nodes.length === 0 ? (
        <EmptyState />
      ) : (
        <JourneyFlow links={report.data.links} nodes={report.data.nodes} />
      )}
    </PageContainer>
  )
}
