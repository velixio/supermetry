import { useEffect, useRef } from 'react'

import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { en } from '@/messages/en'

import { useDashboards, type Dashboard } from './api'
import { builtinKey, dashKey } from './layout'

const t = en.dashView

type DashboardSwitcherProps = {
  siteId: number
  /** The `dash` search value; undefined means "follow the site default". */
  value: string | undefined
  onChange: (key: string, dashboard?: Dashboard) => void
  /** Called once when the URL names no dashboard and the site has a flagged default. */
  onDefault: (key: string, dashboard: Dashboard) => void
}

/** Picks a saved dashboard or the standard one. Hidden while the site has no saved dashboards. */
export function DashboardSwitcher({ siteId, value, onChange, onDefault }: DashboardSwitcherProps) {
  const dashboards = useDashboards(siteId)
  const saved = dashboards.data?.filter((item) => !item.builtin) ?? []
  const flagged = saved.find((item) => item.is_default)

  const onDefaultRef = useRef(onDefault)
  onDefaultRef.current = onDefault
  useEffect(() => {
    if (value === undefined && flagged) onDefaultRef.current(dashKey(flagged.id), flagged)
  }, [value, flagged])

  if (saved.length === 0) return null
  const current = value ?? (flagged ? dashKey(flagged.id) : builtinKey)
  return (
    <div className="flex min-w-0 items-center gap-2">
      <label className="shrink-0 text-sm text-muted-foreground" htmlFor="dashboard-switcher">{t.switcherLabel}</label>
      <Select value={current} onValueChange={(key) => onChange(key, saved.find((item) => dashKey(item.id) === key))}>
        <SelectTrigger className="min-h-11 min-w-0 max-w-full sm:min-h-9 sm:w-64" id="dashboard-switcher">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectItem value={builtinKey}>{t.builtinName}</SelectItem>
            {saved.map((item) => (
              <SelectItem key={item.id} value={dashKey(item.id)}>
                {item.name}{item.is_default ? ` (${t.defaultTag})` : ''}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>
    </div>
  )
}
