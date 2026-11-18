import { Field, FieldDescription, FieldError, FieldLabel, FieldLegend, FieldSet } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

import { CheckField, NumberField, selectClass } from './fieldBits'
import {
  defaultTrigger, errorAt, metricLabels, triggerLabels, usesClosedHours,
  type MetricName, type MetricTrigger, type RuleTrigger, type TriggerType,
} from './ruleTypes'

const issueOn = ['new', 'regressed', 'escalated']
const monitorOn = [['missed', 'Missed'], ['failed', 'Failed'], ['timeout', 'Timed out']] as const

/** The fields of the chosen trigger; the other triggers' fields are not rendered. */
export function TriggerFields({ trigger, onChange, error, monitorSlugs }: {
  trigger: RuleTrigger
  onChange: (trigger: RuleTrigger) => void
  error?: string
  monitorSlugs: string[]
}) {
  return (
    <FieldSet>
      <FieldLegend>Trigger</FieldLegend>
      <Field>
        <FieldLabel htmlFor="rule-trigger-type">When</FieldLabel>
        <select id="rule-trigger-type" className={selectClass} value={trigger.type} onChange={(event) => onChange(defaultTrigger(event.target.value as TriggerType))}>
          {(Object.keys(triggerLabels) as TriggerType[]).map((type) => <option key={type} value={type}>{triggerLabels[type]}</option>)}
        </select>
        {errorAt(error, 'trigger') && !errorAt(error, 'trigger.issue') && !errorAt(error, 'trigger.event') && !errorAt(error, 'trigger.metric') && !errorAt(error, 'trigger.monitor')
          ? <FieldError>{errorAt(error, 'trigger')}</FieldError> : null}
      </Field>
      {trigger.type === 'issue' && trigger.issue ? <IssueFields on={trigger.issue.on} error={errorAt(error, 'trigger.issue.on')} onChange={(on) => onChange({ type: 'issue', issue: { on } })} /> : null}
      {trigger.type === 'event' && trigger.event ? <EventFields trigger={trigger} onChange={onChange} error={error} /> : null}
      {trigger.type === 'metric' && trigger.metric ? <MetricFields metric={trigger.metric} onChange={(metric) => onChange({ type: 'metric', metric })} error={error} /> : null}
      {trigger.type === 'monitor' && trigger.monitor ? (
        <MonitorFields slugs={trigger.monitor.slugs ?? []} on={trigger.monitor.on ?? []} known={monitorSlugs} error={error}
          onChange={(monitor) => onChange({ type: 'monitor', monitor })} />
      ) : null}
    </FieldSet>
  )
}

function toggle(list: string[], value: string, on: boolean) {
  return on ? [...list.filter((item) => item !== value), value] : list.filter((item) => item !== value)
}

function IssueFields({ on, onChange, error }: { on: string[]; onChange: (on: string[]) => void; error?: string }) {
  return (
    <FieldSet>
      <FieldLegend variant="label">Notify when an issue is</FieldLegend>
      {issueOn.map((value) => <CheckField key={value} id={`rule-issue-${value}`} label={value} checked={on.includes(value)} onChange={(checked) => onChange(toggle(on, value, checked))} />)}
      {error ? <FieldError>{error}</FieldError> : null}
    </FieldSet>
  )
}

function EventFields({ trigger, onChange, error }: { trigger: RuleTrigger; onChange: (trigger: RuleTrigger) => void; error?: string }) {
  const event = trigger.event!
  const set = (next: typeof event) => onChange({ type: 'event', event: next })
  const spike = event.spike !== undefined
  return (
    <>
      <CheckField id="rule-event-spike" label="Compare against the hourly baseline (spike) instead of a fixed count" checked={spike}
        onChange={(checked) => set(checked ? { measure: 'events', spike: { factor: 3, min_events: 10, baseline_days: 7 } } : { measure: event.measure, window_minutes: 60, above: 100 })} />
      {spike && event.spike ? (
        <div className="grid gap-3 sm:grid-cols-3">
          <NumberField id="rule-spike-factor" label="Times the baseline" min={1} step={0.5} value={event.spike.factor} error={errorAt(error, 'trigger.event.spike.factor')}
            onChange={(factor) => set({ ...event, spike: { ...event.spike!, factor: factor ?? 0 } })} />
          <NumberField id="rule-spike-min" label="At least events" min={1} value={event.spike.min_events} error={errorAt(error, 'trigger.event.spike.min_events')}
            onChange={(min) => set({ ...event, spike: { ...event.spike!, min_events: min ?? 0 } })} />
          <NumberField id="rule-spike-days" label="Baseline days" min={1} value={event.spike.baseline_days} error={errorAt(error, 'trigger.event.spike.baseline_days')}
            onChange={(days) => set({ ...event, spike: { ...event.spike!, baseline_days: days ?? 0 } })} />
        </div>
      ) : (
        <div className="grid gap-3 sm:grid-cols-3">
          <Field>
            <FieldLabel htmlFor="rule-event-measure">Count</FieldLabel>
            <select id="rule-event-measure" className={selectClass} value={event.measure ?? 'events'} onChange={(e) => set({ ...event, measure: e.target.value as 'events' | 'users' })}>
              <option value="events">Events</option><option value="users">Affected users</option>
            </select>
            {errorAt(error, 'trigger.event.measure') ? <FieldError>{errorAt(error, 'trigger.event.measure')}</FieldError> : null}
          </Field>
          <NumberField id="rule-event-above" label="More than" min={1} value={event.above} error={errorAt(error, 'trigger.event.above')} onChange={(above) => set({ ...event, above })} />
          <NumberField id="rule-event-window" label="Window (minutes)" min={5} value={event.window_minutes} error={errorAt(error, 'trigger.event.window_minutes')} onChange={(window_minutes) => set({ ...event, window_minutes })} />
        </div>
      )}
    </>
  )
}

function MetricFields({ metric, onChange, error }: { metric: MetricTrigger; onChange: (metric: MetricTrigger) => void; error?: string }) {
  const set = (patch: Partial<MetricTrigger>) => onChange({ ...metric, ...patch })
  const p = 'trigger.metric.'
  const closed = usesClosedHours(metric.name)
  return (
    <>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field>
          <FieldLabel htmlFor="rule-metric-name">Metric</FieldLabel>
          <select id="rule-metric-name" className={selectClass} value={metric.name} onChange={(e) => {
            const name = e.target.value as MetricName
            const hours = usesClosedHours(name)
            set({ name, transaction: name.startsWith('transaction_') ? metric.transaction : undefined, window_minutes: hours ? Math.max(60, Math.ceil(metric.window_minutes / 60) * 60) : metric.window_minutes })
          }}>
            {(Object.keys(metricLabels) as MetricName[]).map((name) => <option key={name} value={name}>{metricLabels[name]}</option>)}
          </select>
          {errorAt(error, p + 'name') ? <FieldError>{errorAt(error, p + 'name')}</FieldError> : null}
        </Field>
        <Field>
          <FieldLabel htmlFor="rule-metric-op">Alert when it goes</FieldLabel>
          <select id="rule-metric-op" className={selectClass} value={metric.op} onChange={(e) => set({ op: e.target.value as 'above' | 'below' })}>
            <option value="above">Above</option><option value="below">Below</option>
          </select>
          {errorAt(error, p + 'op') ? <FieldError>{errorAt(error, p + 'op')}</FieldError> : null}
        </Field>
      </div>
      <div className="grid gap-3 sm:grid-cols-3">
        <NumberField id="rule-metric-critical" label="Critical threshold" step={0.1} required value={metric.critical} error={errorAt(error, p + 'critical')} onChange={(critical) => set({ critical: critical ?? 0 })} />
        <NumberField id="rule-metric-warning" label="Warning threshold (optional)" step={0.1} value={metric.warning} error={errorAt(error, p + 'warning')} onChange={(warning) => set({ warning })} />
        <NumberField id="rule-metric-resolve" label="Resolve at (optional)" step={0.1} value={metric.resolve} error={errorAt(error, p + 'resolve')} onChange={(resolve) => set({ resolve })} />
      </div>
      <div className="grid gap-3 sm:grid-cols-3">
        <NumberField id="rule-metric-window" label="Window (minutes)" min={closed ? 60 : 5} step={closed ? 60 : 1} value={metric.window_minutes} error={errorAt(error, p + 'window_minutes')}
          description={closed ? 'Whole hours (60 to 1440).' : undefined} onChange={(window_minutes) => set({ window_minutes: window_minutes ?? 0 })} />
        <NumberField id="rule-metric-sample" label="Minimum sample" min={0} value={metric.min_sample} error={errorAt(error, p + 'min_sample')} onChange={(min_sample) => set({ min_sample })} />
        <NumberField id="rule-metric-renotify" label="Remind every (minutes)" min={0} value={metric.renotify_minutes} error={errorAt(error, p + 'renotify_minutes')} onChange={(renotify_minutes) => set({ renotify_minutes })} />
      </div>
      <Field>
        <FieldLabel htmlFor="rule-metric-comparison">Compare with</FieldLabel>
        <select id="rule-metric-comparison" className={selectClass} value={metric.comparison ?? 'static'} onChange={(e) => {
          const comparison = e.target.value as 'static' | 'change'
          set({ comparison, change_window: comparison === 'change' ? '1d' : undefined })
        }}>
          <option value="static">The threshold values above</option><option value="change">The same window a day or a week earlier (percent change)</option>
        </select>
        {errorAt(error, p + 'comparison') ? <FieldError>{errorAt(error, p + 'comparison')}</FieldError> : null}
      </Field>
      {metric.comparison === 'change' ? (
        <Field>
          <FieldLabel htmlFor="rule-metric-change">Earlier window</FieldLabel>
          <select id="rule-metric-change" className={selectClass} value={metric.change_window ?? '1d'} onChange={(e) => set({ change_window: e.target.value as '1d' | '1w' })}>
            <option value="1d">One day earlier</option><option value="1w">One week earlier</option>
          </select>
          {errorAt(error, p + 'change_window') ? <FieldError>{errorAt(error, p + 'change_window')}</FieldError> : null}
        </Field>
      ) : null}
      {metric.name.startsWith('transaction_') ? (
        <Field>
          <FieldLabel htmlFor="rule-metric-transaction">Transaction (optional)</FieldLabel>
          <Input id="rule-metric-transaction" value={metric.transaction ?? ''} onChange={(e) => set({ transaction: e.target.value || undefined })} />
          {errorAt(error, p + 'transaction') ? <FieldError>{errorAt(error, p + 'transaction')}</FieldError> : null}
        </Field>
      ) : null}
      <Field>
        <FieldLabel htmlFor="rule-metric-release">Release (optional)</FieldLabel>
        <Input id="rule-metric-release" value={metric.release ?? ''} onChange={(e) => set({ release: e.target.value || undefined })} />
        {errorAt(error, p + 'release') ? <FieldError>{errorAt(error, p + 'release')}</FieldError> : null}
      </Field>
    </>
  )
}

function MonitorFields({ slugs, on, known, onChange, error }: {
  slugs: string[]
  on: string[]
  known: string[]
  error?: string
  onChange: (monitor: { slugs: string[]; on: string[] }) => void
}) {
  const options = [...new Set([...known, ...slugs])].sort()
  return (
    <>
      <FieldSet>
        <FieldLegend variant="label">Monitors (none selected means every monitor)</FieldLegend>
        {options.length === 0 ? <FieldDescription>No cron monitors yet. A monitor appears after its first check-in.</FieldDescription> : null}
        {options.map((slug) => <CheckField key={slug} id={`rule-monitor-${slug}`} label={slug} checked={slugs.includes(slug)} onChange={(checked) => onChange({ slugs: toggle(slugs, slug, checked), on })} />)}
        {errorAt(error, 'trigger.monitor.slugs') ? <FieldError>{errorAt(error, 'trigger.monitor.slugs')}</FieldError> : null}
      </FieldSet>
      <FieldSet>
        <FieldLegend variant="label">Notify when a check-in is</FieldLegend>
        {monitorOn.map(([value, label]) => <CheckField key={value} id={`rule-monitor-on-${value}`} label={label} checked={on.includes(value)} onChange={(checked) => onChange({ slugs, on: toggle(on, value, checked) })} />)}
        {errorAt(error, 'trigger.monitor.on') ? <FieldError>{errorAt(error, 'trigger.monitor.on')}</FieldError> : null}
      </FieldSet>
    </>
  )
}
