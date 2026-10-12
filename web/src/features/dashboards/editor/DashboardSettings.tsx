import { XIcon } from 'lucide-react'

import { AddFilterPicker } from '@/components/AddFilterPicker'
import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import type { Filter } from '@/lib/search'
import { dashEdit } from '@/messages/dashEdit'
import { en } from '@/messages/en'

import type { DashboardLayout } from '../api'
import { compares, limits, periods } from './model'

const t = dashEdit

type DashboardSettingsProps = {
  layout: DashboardLayout
  siteId: number
  orgId?: number
  anonymous: boolean
  dateRange: { from: string; to: string }
  onChange: (patch: Partial<Omit<DashboardLayout, 'widgets' | 'schema_version'>>) => void
}

function describe(filter: Filter): string {
  return `${filter.dimension.replace(/_/g, ' ')} ${filter.operator.replace(/_/g, ' ')} ${filter.values.join(', ')}`
}

/** The dashboard-wide default period, comparison and filters. */
export function DashboardSettings({ layout, siteId, orgId, anonymous, dateRange, onChange }: DashboardSettingsProps) {
  const filters = (layout.filters ?? []) as Filter[]
  return (
    <section aria-labelledby="dash-edit-settings" className="flex flex-col gap-3 rounded-xl border bg-card p-4">
      <h2 className="text-sm font-medium" id="dash-edit-settings">{t.settings}</h2>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Field>
          <FieldLabel htmlFor="dash-edit-period">{t.period}</FieldLabel>
          <Select value={layout.period ?? '30d'} onValueChange={(value) => onChange({ period: value as DashboardLayout['period'] })}>
            <SelectTrigger className="min-h-11 w-full sm:min-h-9" id="dash-edit-period"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {periods.map((period) => <SelectItem key={period} value={period}>{en.dashboard.periods[period]}</SelectItem>)}
              </SelectGroup>
            </SelectContent>
          </Select>
        </Field>
        <Field>
          <FieldLabel htmlFor="dash-edit-compare">{t.compare}</FieldLabel>
          <Select value={layout.compare ?? 'off'} onValueChange={(value) => onChange({ compare: value as DashboardLayout['compare'] })}>
            <SelectTrigger className="min-h-11 w-full sm:min-h-9" id="dash-edit-compare"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {compares.map((compare) => <SelectItem key={compare} value={compare}>{t.compareOptions[compare]}</SelectItem>)}
              </SelectGroup>
            </SelectContent>
          </Select>
        </Field>
      </div>
      <div className="flex flex-col gap-2">
        <p className="text-sm font-medium">{t.filters}</p>
        {anonymous ? (
          <FieldDescription>{t.filtersAnonymous}</FieldDescription>
        ) : (
          <>
            {filters.length === 0 ? <FieldDescription>{t.filtersNone}</FieldDescription> : (
              <ul className="flex flex-wrap gap-2">
                {filters.map((filter, index) => (
                  <li className="flex items-center gap-1 rounded-lg border bg-background py-0.5 pl-3 text-sm" key={`${describe(filter)}-${index}`}>
                    <span className="min-w-0 break-words">{describe(filter)}</span>
                    <Button
                      aria-label={t.removeFilter(describe(filter))}
                      className="min-h-11 min-w-11"
                      size="icon"
                      type="button"
                      variant="ghost"
                      onClick={() => onChange({ filters: filters.filter((_, at) => at !== index) })}
                    >
                      <XIcon />
                    </Button>
                  </li>
                ))}
              </ul>
            )}
            <div className="flex flex-wrap items-center gap-2">
              {orgId !== undefined && filters.length < limits.maxFilters ? (
                <AddFilterPicker dateRange={dateRange} orgId={orgId} siteId={siteId} onAdd={(filter) => onChange({ filters: [...filters, filter] })} />
              ) : null}
              <span className="text-xs text-muted-foreground">{t.filtersLimit(limits.maxFilters)}</span>
            </div>
          </>
        )}
      </div>
    </section>
  )
}
