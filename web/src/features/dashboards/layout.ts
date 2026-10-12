import { resolveRange, type Filter, type Period } from '@/lib/search'
import type { StatsQuery } from '@/lib/stats'

import type { DashboardLayout, DashboardWidget } from './api'

export const builtinKey = 'builtin'

/** The `dash` search value for a dashboard id; 0 is the built-in one. */
export function dashKey(id: number): string {
  return id === 0 ? builtinKey : String(id)
}

export function parseDashKey(value: string | undefined): number | undefined {
  if (value === undefined || value === builtinKey) return undefined
  const id = Number(value)
  return Number.isSafeInteger(id) && id > 0 ? id : undefined
}

/** Reading order: top to bottom, then left to right. Stable for ties and overlaps. */
export function readingOrder(widgets: readonly DashboardWidget[]): DashboardWidget[] {
  return widgets
    .map((widget, index) => ({ widget, index }))
    .sort((a, b) => a.widget.y - b.widget.y || a.widget.x - b.widget.x || a.index - b.index)
    .map((entry) => entry.widget)
}

export type ViewerContext = {
  base: StatsQuery
  /** Period chosen in the URL; undefined when the layout default applies. */
  period?: Period
  from?: string
  to?: string
  compare?: StatsQuery['compare']
  timezone: string
  now?: Date
}

/** Filters that apply to one widget: dashboard, URL, then the widget's own. */
export function widgetFilters(layout: DashboardLayout, urlFilters: Filter[], widget: DashboardWidget): Filter[] {
  return [...(layout.filters ?? []), ...urlFilters, ...(widget.filters ?? [])]
}

/** The stats query for a widget, honouring its period override and extra filters. */
export function widgetQuery(
  layout: DashboardLayout,
  widget: DashboardWidget,
  urlFilters: Filter[],
  context: ViewerContext,
): StatsQuery {
  const now = context.now ?? new Date()
  const period: Period = widget.period ?? context.period ?? layout.period ?? '30d'
  const custom = !widget.period && period === 'custom'
  return {
    ...context.base,
    dateRange: resolveRange(period, context.timezone, now, custom ? context.from : undefined, custom ? context.to : undefined),
    filters: widgetFilters(layout, urlFilters, widget),
    compare: context.compare ?? context.base.compare,
  }
}
