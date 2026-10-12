import { useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field, FieldDescription, FieldGroup, FieldLabel, FieldLegend, FieldSet } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { useFunnels } from '@/features/funnels/api'
import { dashEdit } from '@/messages/dashEdit'
import { en } from '@/messages/en'

import type { DashboardWidget } from '../api'
import {
  allMetrics, defaultConfig, dimensions, kindRules, kinds, periods, trafficMetrics, validateDraft,
  type Kind, type Metric,
} from './model'
import { humanize, issueText, widgetLabel } from './text'

const t = dashEdit
const inherit = 'inherit'
const property = '__property'

type WidgetDialogProps = {
  siteId: number
  mode: 'add' | 'edit'
  /** The widget being added (with a fresh id) or edited. */
  widget: DashboardWidget
  onSubmit: (widget: DashboardWidget) => void
  onClose: () => void
}

function Pick({ id, label, value, onChange, options }: {
  id: string
  label: string
  value: string
  onChange: (value: string) => void
  options: Array<{ value: string; label: string }>
}) {
  return (
    <Field>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <Select value={value} onValueChange={onChange}>
        <SelectTrigger className="min-h-11 w-full sm:min-h-9" id={id}><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectGroup>
            {options.map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}
          </SelectGroup>
        </SelectContent>
      </Select>
    </Field>
  )
}

/** Dialog for the kind-specific options of one widget; used to add a widget and to edit it. */
export function WidgetDialog({ siteId, mode, widget, onSubmit, onClose }: WidgetDialogProps) {
  const funnels = useFunnels(siteId)
  const [draft, setDraft] = useState<DashboardWidget>(widget)
  const [propKey, setPropKey] = useState(widget.config?.dimension?.startsWith('prop:') ? widget.config.dimension.slice(5) : '')
  const [errors, setErrors] = useState<string[]>([])
  const rule = kindRules[draft.kind]
  const config = draft.config ?? {}
  const setConfig = (patch: NonNullable<DashboardWidget['config']>) => setDraft({ ...draft, config: { ...config, ...patch } })
  const dimensionChoice = config.dimension?.startsWith('prop:') ? property : (config.dimension ?? 'page')

  const changeKind = (kind: Kind) => {
    setErrors([])
    setDraft({ ...draft, kind, config: defaultConfig(kind, funnels.data?.[0]?.id) })
  }

  const submit = (event: FormEvent) => {
    event.preventDefault()
    const trimmed = draft.title?.trim()
    const candidate: DashboardWidget = { ...draft, title: trimmed ? trimmed : undefined }
    if (!candidate.title) delete candidate.title
    if (rule.limit && config.limit === undefined) candidate.config = { ...config, limit: kindRules[draft.kind].dimension ? 10 : 5 }
    const issues = validateDraft(
      { name: 'x', layout: { schema_version: 1, widgets: [{ ...candidate, y: 0 }] } },
      issueText,
      widgetLabel,
    ).filter((issue) => issue.field === candidate.id)
    if (issues.length > 0) {
      setErrors(issues.map((issue) => issue.message))
      return
    }
    onSubmit(candidate)
  }

  const metricList = rule.metric === 'traffic' ? trafficMetrics : allMetrics
  const toggleMetric = (metric: Metric, on: boolean) => {
    const current = config.metrics ?? []
    setConfig({ metrics: on ? [...current, metric] : current.filter((item) => item !== metric) })
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{mode === 'add' ? t.addTitle : t.editTitle(widgetLabel(widget))}</DialogTitle>
          <DialogDescription>{t.kindNotes[draft.kind]}</DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-4" noValidate onSubmit={submit}>
          <FieldGroup>
            {mode === 'add' ? (
              <Pick id="widget-kind" label={t.kindLabel} value={draft.kind} onChange={(value) => changeKind(value as Kind)}
                options={kinds.map((kind) => ({ value: kind, label: t.kinds[kind] }))} />
            ) : null}
            <Field>
              <FieldLabel htmlFor="widget-title">{t.titleLabel}</FieldLabel>
              <Input className="min-h-11 sm:min-h-9" id="widget-title" maxLength={120} value={draft.title ?? ''} onChange={(event) => setDraft({ ...draft, title: event.target.value })} />
              <FieldDescription>{t.titleHint}</FieldDescription>
            </Field>
            <Pick id="widget-period" label={t.periodLabel} value={draft.period ?? inherit}
              onChange={(value) => setDraft({ ...draft, period: value === inherit ? undefined : (value as DashboardWidget['period']) })}
              options={[{ value: inherit, label: t.periodInherit }, ...periods.map((period) => ({ value: period, label: en.dashboard.periods[period] }))]} />

            {rule.metric ? (
              <Pick id="widget-metric" label={t.metricLabel} value={config.metric ?? 'visitors'} onChange={(value) => setConfig({ metric: value as Metric })}
                options={metricList.map((metric) => ({ value: metric, label: humanize(metric) }))} />
            ) : null}

            {rule.metrics ? (
              <FieldSet>
                <FieldLegend variant="label">{t.metricsLabel(...rule.metrics)}</FieldLegend>
                <div className="grid grid-cols-1 gap-1 sm:grid-cols-2">
                  {allMetrics.map((metric) => {
                    const checked = config.metrics?.includes(metric) ?? false
                    const full = !checked && (config.metrics?.length ?? 0) >= rule.metrics![1]
                    return (
                      <Field className="min-h-11 sm:min-h-9" key={metric} orientation="horizontal">
                        <Checkbox checked={checked} disabled={full} id={`widget-metric-${metric}`} onCheckedChange={(value) => toggleMetric(metric, value === true)} />
                        <FieldLabel htmlFor={`widget-metric-${metric}`}>{humanize(metric)}</FieldLabel>
                      </Field>
                    )
                  })}
                </div>
              </FieldSet>
            ) : null}

            {rule.dimension ? (
              <>
                <Pick id="widget-dimension" label={t.dimensionLabel} value={dimensionChoice}
                  onChange={(value) => {
                    if (value === property) setConfig({ dimension: propKey.trim() ? `prop:${propKey.trim()}` : '' })
                    else setConfig({ dimension: value })
                  }}
                  options={[...dimensions.map((dimension) => ({ value: dimension, label: humanize(dimension) })), { value: property, label: t.propertyOption }]} />
                {dimensionChoice === property ? (
                  <Field>
                    <FieldLabel htmlFor="widget-property">{t.propertyLabel}</FieldLabel>
                    <Input className="min-h-11 sm:min-h-9" id="widget-property" maxLength={55} value={propKey}
                      onChange={(event) => { setPropKey(event.target.value); setConfig({ dimension: event.target.value.trim() ? `prop:${event.target.value.trim()}` : '' }) }} />
                  </Field>
                ) : null}
              </>
            ) : null}

            {rule.limit ? (
              <Field>
                <FieldLabel htmlFor="widget-limit">{t.limitLabel}</FieldLabel>
                <Input className="min-h-11 sm:min-h-9" id="widget-limit" inputMode="numeric" max={50} min={1} type="number"
                  value={config.limit ?? ''} onChange={(event) => setConfig({ limit: event.target.value === '' ? undefined : Number(event.target.value) })} />
              </Field>
            ) : null}

            {rule.funnel ? (
              funnels.data && funnels.data.length === 0 ? (
                <p className="text-sm text-muted-foreground" role="status">{t.funnelNone}</p>
              ) : (
                <Field>
                  <FieldLabel htmlFor="widget-funnel">{t.funnelLabel}</FieldLabel>
                  <Select value={config.funnel_id ? String(config.funnel_id) : ''} onValueChange={(value) => setConfig({ funnel_id: Number(value) })}>
                    <SelectTrigger className="min-h-11 w-full sm:min-h-9" id="widget-funnel"><SelectValue placeholder={t.funnelPlaceholder} /></SelectTrigger>
                    <SelectContent>
                      <SelectGroup>
                        {(funnels.data ?? []).map((funnel) => <SelectItem key={funnel.id} value={String(funnel.id)}>{funnel.name}</SelectItem>)}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                </Field>
              )
            ) : null}
          </FieldGroup>
          {errors.length > 0 ? (
            <ul className="list-disc pl-5 text-sm text-destructive" role="alert">{errors.map((message) => <li key={message}>{message}</li>)}</ul>
          ) : null}
          <DialogFooter className="gap-2">
            <Button className="min-h-11 sm:min-h-9" type="button" variant="outline" onClick={onClose}>{t.cancel}</Button>
            <Button className="min-h-11 sm:min-h-9" type="submit">{mode === 'add' ? t.addConfirm : t.apply}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
