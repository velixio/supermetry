import { DropdownMenuGroup, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator } from '@/components/ui/dropdown-menu'
import type { Filter } from '@/lib/search'

import { useCohorts } from './api'

export function isCohortFilter(filter: Filter): boolean {
  return filter.dimension === 'cohort'
}

// withCohort replaces any cohort filter, so only one audience applies at a time.
export function withCohort(filters: Filter[], id: number): Filter[] {
  return [...filters.filter((filter) => !isCohortFilter(filter)), { dimension: 'cohort', operator: 'is', values: [String(id)] }]
}

// CohortMenuItems lists saved cohorts inside the open segments menu. It mounts
// only while the menu is open, so closed dashboards never request cohorts.
export function CohortMenuItems({ siteId, filters, onApply }: { siteId: number; filters: Filter[]; onApply: (filters: Filter[]) => void }) {
  const cohorts = useCohorts(siteId)
  if (cohorts.isError) return null
  return (
    <>
      <DropdownMenuSeparator />
      <DropdownMenuLabel>Cohorts</DropdownMenuLabel>
      <DropdownMenuGroup>
        {cohorts.isPending ? <DropdownMenuItem disabled>Loading…</DropdownMenuItem> : null}
        {cohorts.data?.length === 0 ? <DropdownMenuItem disabled>No saved cohorts</DropdownMenuItem> : null}
        {cohorts.data?.map((cohort) => (
          <DropdownMenuItem key={cohort.id} onSelect={() => onApply(withCohort(filters, cohort.id))}>
            {cohort.name}
          </DropdownMenuItem>
        ))}
      </DropdownMenuGroup>
    </>
  )
}

// useCohortFilterName resolves the name shown on a cohort filter chip. The
// list is requested only while such a filter is active.
export function useCohortFilterName(siteId: number, filters: Filter[]): (filter: Filter) => string {
  const active = filters.some(isCohortFilter)
  const cohorts = useCohorts(siteId, active)
  return (filter) => {
    const id = Number(filter.values[0])
    return cohorts.data?.find((cohort) => cohort.id === id)?.name ?? `#${filter.values[0] ?? ''}`
  }
}
