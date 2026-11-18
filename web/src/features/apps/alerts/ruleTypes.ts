// Types and helpers of the alert rule document (internal/alerts/rule.go).

import { uiAlerts as t } from '@/messages/uiAlerts'

export type TriggerType = 'issue' | 'event' | 'metric' | 'monitor'
export type MetricName =
  | 'error_count' | 'crash_free_sessions' | 'crash_free_users'
  | 'transaction_count' | 'transaction_failure_rate'
  | 'transaction_p50' | 'transaction_p75' | 'transaction_p95' | 'transaction_p99'

export type Spike = { factor: number; min_events: number; baseline_days: number }
export type MetricTrigger = {
  name: MetricName
  transaction?: string
  op: 'above' | 'below'
  critical: number
  warning?: number
  resolve?: number
  comparison?: 'static' | 'change'
  change_window?: '1d' | '1w'
  window_minutes: number
  min_sample?: number
  release?: string
  renotify_minutes?: number
}
export type RuleTrigger = {
  type: TriggerType
  issue?: { on: string[] }
  event?: { measure?: 'events' | 'users'; window_minutes?: number; above?: number; spike?: Spike }
  metric?: MetricTrigger
  monitor?: { slugs?: string[]; on?: string[] }
}
export type TagFilter = { key: string; value: string; negate?: boolean }
export type RuleFilters = {
  environments?: string[]
  releases?: string[]
  platforms?: string[]
  levels?: string[]
  kinds?: string[]
  tags?: TagFilter[]
  owner?: { user_id: number }
}
export type ActionType = 'channel' | 'webhook' | 'assign' | 'notify_owner'
export type RuleAction = { type: ActionType; channel_ids?: number[]; user_id?: number; fallback_channel_ids?: number[] }
export type RuleDoc = {
  trigger: RuleTrigger
  filters: RuleFilters
  actions: RuleAction[]
  frequency_minutes?: number
  notify_on_resolve?: boolean
}

// Live catalogue objects: translations are applied in place, so these always show the active language.
export const metricLabels: Record<MetricName, string> = t.rules.metrics

export const triggerLabels: Record<TriggerType, string> = t.rules.triggers

export const usesClosedHours = (name: MetricName) => name.startsWith('transaction_') || name.startsWith('crash_free_')

export function defaultTrigger(type: TriggerType): RuleTrigger {
  switch (type) {
    case 'issue': return { type, issue: { on: ['new'] } }
    case 'event': return { type, event: { measure: 'events', window_minutes: 60, above: 100 } }
    case 'metric': return { type, metric: { name: 'error_count', op: 'above', critical: 100, comparison: 'static', window_minutes: 60 } }
    case 'monitor': return { type, monitor: { on: ['missed', 'failed', 'timeout'] } }
  }
}

export const blankRule = (): RuleDoc => ({ trigger: defaultTrigger('issue'), filters: {}, actions: [{ type: 'channel', channel_ids: [] }] })

/** Metric and monitor triggers drive incidents: no assign or notify_owner actions, no tag or owner filters. */
export const isIncidental = (type: TriggerType) => type === 'metric' || type === 'monitor'

/**
 * The API names the offending field at the start of a 400 detail:
 * "invalid alert configuration: trigger.metric.window_minutes must be ...".
 */
export function parseFieldError(message: string | undefined): { path: string; text: string } | null {
  if (!message) return null
  const match = /^(?:invalid alert configuration: )?([a-z_]+(?:\.[a-z_]+|\[\d+\])*(?:\.[a-z_]+)*) (.+)$/.exec(message)
  return match ? { path: match[1], text: match[2] } : null
}

/** The error text for a path (or any path below it), or undefined. */
export function errorAt(message: string | undefined, path: string): string | undefined {
  const parsed = parseFieldError(message)
  if (!parsed) return undefined
  return parsed.path === path || parsed.path.startsWith(`${path}.`) || parsed.path.startsWith(`${path}[`) ? parsed.text : undefined
}

export function describeRule(rule: RuleDoc | undefined): string {
  if (!rule) return ''
  const tr = rule.trigger
  const d = t.rules.describe
  switch (tr.type) {
    case 'issue': return d.issue(tr.issue?.on.join(', ') ?? '')
    case 'event': return tr.event?.spike ? d.spike : d.event(tr.event?.above ?? '', tr.event?.measure ?? 'events', tr.event?.window_minutes ?? '')
    case 'metric': return d.metric(metricLabels[tr.metric!.name], tr.metric!.op, tr.metric!.critical)
    case 'monitor': return d.monitor(tr.monitor?.slugs?.length ? tr.monitor.slugs.join(', ') : d.anyMonitor, (tr.monitor?.on ?? []).join(', '))
  }
}
