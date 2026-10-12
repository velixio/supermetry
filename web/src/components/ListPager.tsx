import { Button } from '@/components/ui/button'
import { ui } from '@/messages/ui'

export function ListPager({
  page,
  pageSize,
  rowCount,
  total,
  totalCapped,
  hasNext,
  fetching,
  onFirst,
  onPrevious,
  onNext,
}: {
  page: number
  pageSize: number
  rowCount: number
  total?: number
  totalCapped?: boolean
  hasNext: boolean
  fetching: boolean
  onFirst: () => void
  onPrevious: () => void
  onNext: () => void
}) {
  const empty = rowCount === 0
  const first = (page - 1) * pageSize + 1
  const last = first + rowCount - 1
  const totalLabel = total === undefined ? undefined : totalCapped ? '10000+' : total.toLocaleString()
  const pages = total === undefined ? undefined : Math.ceil(total / pageSize)
  const pagesLabel = pages === undefined ? undefined : totalCapped ? `${pages}+` : pages.toLocaleString()
  const summary = empty
    ? ui.pager.noResults
    : `${ui.pager.showing(first.toLocaleString(), last.toLocaleString())}${totalLabel === undefined ? '' : ui.pager.ofTotal(totalLabel)}${ui.pager.page(page.toLocaleString())}${pagesLabel === undefined ? '' : ui.pager.ofPages(pagesLabel)}`

  return (
    <nav aria-label={ui.pager.label} className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <p aria-live="polite" className="text-sm text-muted-foreground">{summary}</p>
      <div className="flex flex-wrap gap-2">
        <Button disabled={empty || page <= 1 || fetching} type="button" variant="outline" onClick={onFirst}>{ui.pager.first}</Button>
        <Button disabled={empty || page <= 1 || fetching} type="button" variant="outline" onClick={onPrevious}>{ui.pager.previous}</Button>
        <Button disabled={empty || !hasNext || fetching} type="button" variant="outline" onClick={onNext}>{ui.pager.next}</Button>
      </div>
    </nav>
  )
}
