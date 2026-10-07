import type { Filter } from '../lib/search'

type FilterBarProps = {
  filters: Filter[]
  dropped: string[]
  invalidNotice: string
  filterLabel: (filter: Filter) => string
  removeLabel: (filter: Filter) => string
  onRemove: (index: number) => void
}

export function FilterBar({
  filters,
  dropped,
  invalidNotice,
  filterLabel,
  removeLabel,
  onRemove,
}: FilterBarProps) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      {dropped.length > 0 ? (
        <p
          aria-live="polite"
          className="fixed right-4 bottom-4 rounded-md border bg-popover px-4 py-3 text-sm text-popover-foreground shadow-md"
          role="status"
        >
          {invalidNotice}
        </p>
      ) : null}
      {filters.map((filter, index) => (
        <button
          key={`${filter.dimension}:${filter.operator}:${filter.values.join(',')}:${index}`}
          aria-label={removeLabel(filter)}
          className="inline-flex h-8 items-center rounded-md border bg-secondary px-3 text-sm text-secondary-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring"
          type="button"
          onClick={() => onRemove(index)}
        >
          {filterLabel(filter)}
          <span aria-hidden="true" className="ml-2 text-muted-foreground">
            ×
          </span>
        </button>
      ))}
    </div>
  )
}
