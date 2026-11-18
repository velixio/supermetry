import { useEffect, useRef } from 'react'

import { ChevronDownIcon, LayoutDashboardIcon, PlusIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { dashboardView } from '@/messages/dashboardView'
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
  /** Owner, admin or editor: the menu then ends with "New dashboard". Viewers never see it. */
  canWrite?: boolean
  onNew?: () => void
}

/** Picks a saved dashboard or the standard one. Hidden while the site has no saved dashboards. */
export function DashboardSwitcher({ siteId, value, onChange, onDefault, canWrite = false, onNew }: DashboardSwitcherProps) {
  const dashboards = useDashboards(siteId)
  const saved = dashboards.data?.filter((item) => !item.builtin) ?? []
  const flagged = saved.find((item) => item.is_default)

  const onDefaultRef = useRef(onDefault)
  onDefaultRef.current = onDefault
  useEffect(() => {
    if (value === undefined && flagged) onDefaultRef.current(dashKey(flagged.id), flagged)
  }, [value, flagged])

  // A viewer on a site with no saved dashboards has nothing to switch to.
  if (saved.length === 0 && !canWrite) return null
  const current = value ?? (flagged ? dashKey(flagged.id) : builtinKey)
  const currentName = current === builtinKey ? t.builtinName : saved.find((item) => dashKey(item.id) === current)?.name ?? t.builtinName
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button aria-label={`${dashboardView.switcher.trigger}: ${currentName}`} className="max-sm:min-h-11 max-sm:shrink min-w-0 max-w-full justify-between gap-1.5 px-2.5 text-muted-foreground hover:text-foreground data-[state=open]:text-foreground sm:min-h-9 sm:max-w-64" type="button" variant="ghost">
          <LayoutDashboardIcon aria-hidden="true" className="sm:hidden" />
          <span className="truncate max-sm:sr-only">{currentName}</span>
          <ChevronDownIcon aria-hidden="true" className="shrink-0 max-sm:hidden" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="min-w-56">
        <DropdownMenuLabel>{dashboardView.switcher.label}</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={current} onValueChange={(key) => onChange(key, saved.find((item) => dashKey(item.id) === key))}>
          <DropdownMenuRadioItem value={builtinKey}>{t.builtinName}</DropdownMenuRadioItem>
          {saved.map((item) => (
            <DropdownMenuRadioItem key={item.id} value={dashKey(item.id)}>
              {item.name}{item.is_default ? ` (${t.defaultTag})` : ''}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
        {canWrite && onNew ? (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={onNew}>
              <PlusIcon aria-hidden="true" />
              {dashboardView.switcher.newDashboard}
            </DropdownMenuItem>
          </>
        ) : null}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
