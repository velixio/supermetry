import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import { Flag } from '@/components/Flag'
import { useIsMobile } from '@/hooks/use-mobile'
import { countryLabel } from '@/lib/country'
import { fmt } from '@/lib/format'
import { useBreakdown, useStats, type StatsQuery } from '@/lib/stats'
import { en } from '@/messages/en'

type CountryDetailsProps = {
  country: string
  query: StatsQuery
  onClear(): void
  onFilter(): void
}

const detailDimensions = [
  ['page', en.map.topPages],
  ['region', en.map.regions],
  ['city', en.map.cities],
  ['source', en.map.sources],
] as const

function CountryDetailsBody({ country, query, onClear, onFilter }: CountryDetailsProps) {
  const countryFilter = {
    dimension: 'country',
    operator: 'is' as const,
    values: [country],
  }
  const filteredQuery = {
    ...query,
    filters: [...query.filters, countryFilter],
  }
  const summary = useStats({
    ...filteredQuery,
    metrics: ['visitors', 'pageviews', 'bounce_rate', 'visit_duration'],
    dimensions: undefined,
  })
  const pages = useBreakdown({
    ...filteredQuery,
    metrics: [query.metrics[0]],
    dimensions: ['page'],
    limit: 5,
  })
  const regions = useBreakdown({
    ...filteredQuery,
    metrics: [query.metrics[0]],
    dimensions: ['region'],
    limit: 5,
  })
  const cities = useBreakdown({
    ...filteredQuery,
    metrics: [query.metrics[0]],
    dimensions: ['city'],
    limit: 5,
  })
  const sources = useBreakdown({
    ...filteredQuery,
    metrics: [query.metrics[0]],
    dimensions: ['source'],
    limit: 5,
  })
  const breakdowns = [pages, regions, cities, sources]
  const pending = summary.isPending || breakdowns.some((result) => result.isPending)
  const failed = summary.isError || breakdowns.some((result) => result.isError)
  const totals = summary.data?.totals ?? {}
  const empty =
    Number(totals.visitors ?? 0) === 0 && breakdowns.every((result) => (result.data?.rows.length ?? 0) === 0)

  if (pending)
    return (
      <div aria-label={en.map.detailsLoading} className="flex flex-col gap-3 p-4">
        {Array.from({ length: 5 }, (_, index) => (
          <Skeleton key={index} className="h-8 w-full" />
        ))}
      </div>
    )
  if (failed)
    return (
      <div className="flex flex-col items-start gap-3 p-4" role="alert">
        <p className="text-sm text-muted-foreground">{en.map.detailsError}</p>
        <Button
          size="sm"
          variant="outline"
          onClick={() => {
            void summary.refetch()
            for (const result of breakdowns) void result.refetch()
          }}
        >
          {en.map.retry}
        </Button>
      </div>
    )
  if (empty)
    return (
      <div className="flex flex-col gap-3 p-4">
        <p className="text-sm text-muted-foreground">{en.map.detailsEmpty}</p>
        <Button size="sm" variant="ghost" onClick={onClear}>
          {en.map.clearSelection}
        </Button>
      </div>
    )

  const metrics = [
    [en.map.visitors, fmt.number(Number(totals.visitors ?? 0))],
    [en.map.pageviews, fmt.number(Number(totals.pageviews ?? 0))],
    [en.map.bounceRate, fmt.percent(Number(totals.bounce_rate ?? 0))],
    [en.map.visitDuration, fmt.duration(Number(totals.visit_duration ?? 0))],
  ]

  return (
    <>
      <div className="grid grid-cols-2 gap-2 p-4 lg:grid-cols-4">
        {metrics.map(([label, value]) => (
          <div key={label} className="rounded-lg bg-muted/50 p-3">
            <p className="text-xs text-muted-foreground">{label}</p>
            <p className="mt-1 font-medium tabular-nums">{value}</p>
          </div>
        ))}
      </div>
      <div className="grid gap-4 border-t p-4 sm:grid-cols-2 lg:grid-cols-4">
        {detailDimensions.map(([dimension, title], index) => (
          <section key={dimension} aria-label={title}>
            <h3 className="text-sm font-medium">{title}</h3>
            {(breakdowns[index].data?.rows.length ?? 0) > 0 ? (
              <ol className="mt-2 flex flex-col gap-1">
                {breakdowns[index].data?.rows.slice(0, 5).map((row, rowIndex) => (
                  <li
                    key={`${String(row[dimension])}:${rowIndex}`}
                    className="flex min-w-0 items-center justify-between gap-2 text-sm"
                  >
                    <span className="truncate text-muted-foreground">{String(row[dimension] || en.map.unknown)}</span>
                    <span className="tabular-nums">{fmt.number(Number(row[query.metrics[0]] ?? 0))}</span>
                  </li>
                ))}
              </ol>
            ) : (
              <p className="mt-2 text-sm text-muted-foreground">{en.map.detailsEmpty}</p>
            )}
          </section>
        ))}
      </div>
      <div className="flex flex-wrap gap-2 border-t p-4">
        <Button onClick={onFilter}>{en.map.filterDashboard}</Button>
        <Button variant="ghost" onClick={onClear}>
          {en.map.clearSelection}
        </Button>
      </div>
    </>
  )
}

export function CountryDetails(props: CountryDetailsProps) {
  const mobile = useIsMobile()
  const title = (
    <span className="flex items-center gap-2">
      <Flag code={props.country} />
      {countryLabel(props.country)}
    </span>
  )
  if (mobile) {
    return (
      <Sheet
        open
        onOpenChange={(open) => {
          if (!open) props.onClear()
        }}
      >
        <SheetContent
          className="max-h-[88dvh] overflow-y-auto bg-popover pb-[max(1rem,env(safe-area-inset-bottom))] data-open:animate-none data-closed:animate-none"
          side="bottom"
        >
          <SheetHeader>
            <SheetTitle>{title}</SheetTitle>
            <SheetDescription>{en.map.details}</SheetDescription>
          </SheetHeader>
          <CountryDetailsBody {...props} />
        </SheetContent>
      </Sheet>
    )
  }
  return (
    <section className="min-w-0 border-t bg-card" aria-label={en.map.details}>
      <header className="p-4"><h3 className="text-sm font-medium">{title}</h3></header>
      <CountryDetailsBody {...props} />
    </section>
  )
}
