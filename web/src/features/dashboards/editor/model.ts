import type { components } from '@/lib/api.gen'

import type { DashboardLayout, DashboardWidget } from '../api'

/** The limits the server enforces on every layout; the editor checks the same ones before saving. */
export const limits = {
  maxWidgets: 24,
  columns: 12,
  maxRows: 200,
  maxHeight: 12,
  maxBytes: 32 * 1024,
  maxFilters: 10,
  maxTitle: 80,
  maxName: 80,
  maxId: 40,
  maxDashboards: 50,
} as const

export type Metric = components['schemas']['Metric']
export type Kind = DashboardWidget['kind']
export type WidgetPeriod = NonNullable<DashboardLayout['period']>

export const kinds: readonly Kind[] = [
  'kpi_strip', 'time_series', 'breakdown', 'map', 'weekly_rhythm', 'goals', 'funnel', 'retention', 'web_vitals',
]

export const allMetrics: readonly Metric[] = [
  'visitors', 'visits', 'pageviews', 'views_per_visit', 'bounce_rate', 'visit_duration', 'engagement_time',
  'exit_rate', 'time_on_page', 'scroll_depth', 'events', 'conversions', 'conversion_rate', 'total_revenue', 'average_revenue',
]
export const trafficMetrics: readonly Metric[] = ['visitors', 'pageviews']

export const dimensions: readonly string[] = [
  'page', 'entry', 'exit', 'source', 'channel', 'country', 'region', 'city', 'browser', 'os', 'device',
  'screen_size', 'language', 'hostname', 'utm_source', 'utm_medium', 'utm_campaign',
]

export const periods: readonly WidgetPeriod[] = [
  'today', 'yesterday', '7d', '28d', '30d', '91d', '6mo', '12mo', 'month', 'last_month', 'year',
]
export const compares = ['off', 'previous_period', 'previous_year'] as const

/** Per-kind metric rules, mirroring the server's table of accepted config fields. */
export const kindRules: Record<Kind, { metrics?: [number, number]; metric?: 'any' | 'traffic'; dimension?: boolean; limit?: boolean; funnel?: boolean }> = {
  kpi_strip: { metrics: [1, 6] },
  time_series: { metric: 'any' },
  breakdown: { metrics: [1, 4], dimension: true, limit: true },
  map: { metric: 'traffic' },
  weekly_rhythm: { metric: 'traffic' },
  goals: { limit: true },
  funnel: { funnel: true },
  retention: {},
  web_vitals: {},
}

const sizes: Record<Kind, { w: number; h: number; minW: number }> = {
  kpi_strip: { w: 12, h: 2, minW: 4 },
  time_series: { w: 12, h: 5, minW: 4 },
  breakdown: { w: 6, h: 5, minW: 3 },
  map: { w: 7, h: 6, minW: 4 },
  weekly_rhythm: { w: 5, h: 6, minW: 3 },
  goals: { w: 6, h: 4, minW: 3 },
  funnel: { w: 6, h: 4, minW: 3 },
  retention: { w: 6, h: 5, minW: 4 },
  web_vitals: { w: 6, h: 4, minW: 3 },
}

export function defaultSize(kind: Kind): { w: number; h: number } {
  return { w: sizes[kind].w, h: sizes[kind].h }
}

export function minWidth(kind: Kind): number {
  return sizes[kind].minW
}

export type Draft = { name: string; layout: DashboardLayout }

/** Reading order, then placed left to right in rows so list order and grid order always agree. */
export function packWidgets(widgets: readonly DashboardWidget[]): DashboardWidget[] {
  let x = 0
  let y = 0
  let rowHeight = 0
  return widgets.map((widget) => {
    if (x + widget.w > limits.columns) {
      y += rowHeight
      x = 0
      rowHeight = 0
    }
    const placed = { ...widget, x, y }
    x += widget.w
    rowHeight = Math.max(rowHeight, widget.h)
    return placed
  })
}

function ordered(widgets: readonly DashboardWidget[]): DashboardWidget[] {
  return widgets
    .map((widget, index) => ({ widget, index }))
    .sort((a, b) => a.widget.y - b.widget.y || a.widget.x - b.widget.x || a.index - b.index)
    .map((entry) => entry.widget)
}

/** The editable form of a saved layout: reading order, packed. */
export function normalizeLayout(layout: DashboardLayout): DashboardLayout {
  return { ...layout, widgets: packWidgets(ordered(layout.widgets)) }
}

export function defaultConfig(kind: Kind, funnelId?: number): DashboardWidget['config'] {
  switch (kind) {
    case 'kpi_strip': return { metrics: ['visitors', 'pageviews', 'bounce_rate'] }
    case 'time_series': return { metric: 'visitors' }
    case 'breakdown': return { dimension: 'page', metrics: ['visitors'], limit: 10 }
    case 'map':
    case 'weekly_rhythm': return { metric: 'visitors' }
    case 'goals': return { limit: 5 }
    case 'funnel': return funnelId === undefined ? {} : { funnel_id: funnelId }
    default: return undefined
  }
}

export function newWidgetId(layout: DashboardLayout, kind: Kind): string {
  const taken = new Set(layout.widgets.map((widget) => widget.id))
  const base = kind.replace(/_/g, '-')
  for (let n = 1; ; n += 1) {
    const id = `${base}-${n}`
    if (!taken.has(id)) return id
  }
}

export function makeWidget(layout: DashboardLayout, kind: Kind, extra: Partial<DashboardWidget> = {}): DashboardWidget {
  const size = sizes[kind]
  const config = extra.config ?? defaultConfig(kind)
  return { id: newWidgetId(layout, kind), kind, x: 0, y: 0, w: size.w, h: size.h, ...(config ? { config } : {}), ...extra }
}

export type EditorAction =
  | { type: 'add'; widget: DashboardWidget }
  | { type: 'remove'; id: string }
  | { type: 'move'; id: string; delta: -1 | 1 }
  | { type: 'resize'; id: string; w: number }
  | { type: 'update'; id: string; patch: Partial<DashboardWidget> }
  | { type: 'rename'; name: string }
  | { type: 'layout'; patch: Partial<Omit<DashboardLayout, 'widgets' | 'schema_version'>> }
  | { type: 'reset'; draft: Draft }

export function reduceDraft(draft: Draft, action: EditorAction): Draft {
  const widgets = draft.layout.widgets
  const withWidgets = (next: DashboardWidget[]): Draft => ({ ...draft, layout: { ...draft.layout, widgets: packWidgets(next) } })
  switch (action.type) {
    case 'add':
      return withWidgets([...widgets, action.widget])
    case 'remove':
      return withWidgets(widgets.filter((widget) => widget.id !== action.id))
    case 'move': {
      const from = widgets.findIndex((widget) => widget.id === action.id)
      const to = from + action.delta
      if (from < 0 || to < 0 || to >= widgets.length) return draft
      const next = [...widgets]
      ;[next[from], next[to]] = [next[to], next[from]]
      return withWidgets(next)
    }
    case 'resize':
      return withWidgets(widgets.map((widget) => (
        widget.id === action.id ? { ...widget, w: Math.min(limits.columns, Math.max(minWidth(widget.kind), action.w)) } : widget
      )))
    case 'update':
      return withWidgets(widgets.map((widget) => (widget.id === action.id ? { ...widget, ...action.patch, id: widget.id } : widget)))
    case 'rename':
      return { ...draft, name: action.name }
    case 'layout':
      return { ...draft, layout: { ...draft.layout, ...action.patch } }
    case 'reset':
      return action.draft
  }
}

const canonical = (draft: Draft) => JSON.stringify({ name: draft.name.trim(), layout: draft.layout })

export function isDirty(draft: Draft, baseline: Draft): boolean {
  return canonical(draft) !== canonical(baseline)
}

export type Issue = { field: string; message: string }
export type IssueText = {
  name: string
  tooMany: (max: number) => string
  tooLarge: string
  tooTall: string
  title: (label: string) => string
  filters: (max: number) => string
  metrics: (label: string, min: number, max: number) => string
  dimension: (label: string) => string
  funnel: (label: string) => string
  limit: (label: string) => string
}

export function byteLength(layout: DashboardLayout): number {
  return new TextEncoder().encode(JSON.stringify(layout)).length
}

/** Client-side mirror of the server rules. An empty list means the draft can be saved. */
export function validateDraft(draft: Draft, text: IssueText, label: (widget: DashboardWidget) => string): Issue[] {
  const issues: Issue[] = []
  const name = draft.name.trim()
  if (name === '' || [...name].length > limits.maxName) issues.push({ field: 'name', message: text.name })
  const { widgets } = draft.layout
  if (widgets.length > limits.maxWidgets) issues.push({ field: 'widgets', message: text.tooMany(limits.maxWidgets) })
  if ((draft.layout.filters?.length ?? 0) > limits.maxFilters) issues.push({ field: 'filters', message: text.filters(limits.maxFilters) })
  for (const widget of widgets) {
    const name = label(widget)
    const rule = kindRules[widget.kind]
    const config = widget.config ?? {}
    if ([...(widget.title ?? '')].length > limits.maxTitle) issues.push({ field: widget.id, message: text.title(name) })
    if ((widget.filters?.length ?? 0) > limits.maxFilters) issues.push({ field: widget.id, message: text.filters(limits.maxFilters) })
    if (widget.y >= limits.maxRows) issues.push({ field: widget.id, message: text.tooTall })
    if (rule.metrics) {
      const count = config.metrics?.length ?? 0
      if (count < rule.metrics[0] || count > rule.metrics[1]) issues.push({ field: widget.id, message: text.metrics(name, ...rule.metrics) })
    }
    if (rule.dimension && !config.dimension) issues.push({ field: widget.id, message: text.dimension(name) })
    if (rule.funnel && !config.funnel_id) issues.push({ field: widget.id, message: text.funnel(name) })
    if (rule.limit && config.limit !== undefined && (!Number.isInteger(config.limit) || config.limit < 1 || config.limit > 50)) {
      issues.push({ field: widget.id, message: text.limit(name) })
    }
  }
  if (byteLength(draft.layout) > limits.maxBytes) issues.push({ field: 'layout', message: text.tooLarge })
  return issues
}

function template(widgets: Array<Pick<DashboardWidget, 'kind'> & Partial<DashboardWidget>>, period: WidgetPeriod = '30d'): DashboardLayout {
  const layout: DashboardLayout = { schema_version: 1, period, compare: 'off', widgets: [] }
  for (const entry of widgets) layout.widgets.push(makeWidget(layout, entry.kind, entry))
  return { ...layout, widgets: packWidgets(layout.widgets) }
}

const breakdown = (dimension: string, title: string, metrics: Metric[] = ['visitors']): Partial<DashboardWidget> & { kind: Kind } => ({
  kind: 'breakdown', title, w: 6, config: { dimension, metrics, limit: 10 },
})

export type TemplateKey = 'standard' | 'marketing' | 'product' | 'performance' | 'blank'

/** Starting points for a new dashboard; the standard one mirrors the built-in layout. */
export function templateLayout(key: TemplateKey): DashboardLayout {
  switch (key) {
    case 'standard':
      return template([
        { kind: 'kpi_strip', config: { metrics: ['visitors', 'visits', 'pageviews', 'views_per_visit', 'bounce_rate', 'visit_duration'] } },
        { kind: 'time_series' },
        breakdown('page', 'Pages'), breakdown('source', 'Sources'), breakdown('country', 'Location'), breakdown('device', 'Environment'),
        { kind: 'map' }, { kind: 'weekly_rhythm' }, { kind: 'goals', w: 12 },
      ])
    case 'marketing':
      return template([
        { kind: 'kpi_strip', config: { metrics: ['visitors', 'visits', 'bounce_rate'] } },
        { kind: 'time_series' },
        breakdown('channel', 'Channels'), breakdown('source', 'Sources'), breakdown('utm_campaign', 'Campaigns'), breakdown('page', 'Landing pages'),
        { kind: 'goals', w: 12 },
      ])
    case 'product':
      return template([
        { kind: 'kpi_strip', config: { metrics: ['visitors', 'views_per_visit', 'visit_duration', 'bounce_rate'] } },
        { kind: 'time_series', config: { metric: 'pageviews' } },
        breakdown('page', 'Pages'), breakdown('device', 'Devices'), { kind: 'retention' }, { kind: 'weekly_rhythm' }, { kind: 'goals', w: 12 },
      ], '91d')
    case 'performance':
      return template([
        { kind: 'web_vitals', w: 12 },
        { kind: 'time_series' },
        breakdown('page', 'Pages'), breakdown('browser', 'Browsers'), breakdown('device', 'Devices'), breakdown('country', 'Countries'),
      ], '28d')
    case 'blank':
      return { schema_version: 1, period: '30d', compare: 'off', widgets: [] }
  }
}
