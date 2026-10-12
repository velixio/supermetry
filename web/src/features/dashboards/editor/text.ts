import { dashEdit } from '@/messages/dashEdit'

import type { DashboardWidget } from '../api'
import { widgetTitle } from '../widgets'
import type { IssueText } from './model'

const t = dashEdit

export function humanize(value: string): string {
  if (value.startsWith('prop:')) return value.slice(5)
  const words = value.replace(/_/g, ' ')
  return words.charAt(0).toUpperCase() + words.slice(1)
}

/** The name a widget goes by in labels and announcements. */
export function widgetLabel(widget: DashboardWidget): string {
  return widget.title?.trim() || widgetTitle(widget)
}

export const issueText: IssueText = {
  name: t.nameInvalid,
  tooMany: t.issues.tooMany,
  tooLarge: t.issues.tooLarge,
  tooTall: t.issues.tooTall,
  title: t.issues.title,
  filters: t.issues.filters,
  metrics: t.issues.metrics,
  dimension: t.issues.dimension,
  funnel: t.issues.funnel,
  limit: t.issues.limit,
}
