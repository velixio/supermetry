import { useState } from 'react'

import { InfoTip } from '@/components/InfoTip'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

import type { AppSearch } from '../search'
import { formatShare, parseFeatureNames, useCreateFeatureSet, useDeleteFeatureSet, useFeatureAdoption, useFeatureSets, type AppFeatureSetData } from './api'

function FeatureRow({ siteId, feature, search, timezone, canEdit }: { siteId: number; feature: AppFeatureSetData; search: AppSearch; timezone: string; canEdit: boolean }) {
  const adoption = useFeatureAdoption(siteId, feature.id, search, timezone)
  const remove = useDeleteFeatureSet(siteId)
  return (
    <li className="flex flex-wrap items-center justify-between gap-3 rounded-lg bg-card p-3">
      <div className="min-w-0">
        <p className="font-medium">{feature.name}</p>
        <p className="truncate text-xs text-muted-foreground">{feature.event_names.join(', ')}</p>
      </div>
      <div className="flex items-center gap-3">
        <span className="text-sm tabular-nums" aria-label={`${feature.name} adoption`}>
          {adoption.data ? `${formatShare(adoption.data.share)} (${adoption.data.users} of ${adoption.data.active_users})` : '…'}
        </span>
        {canEdit ? <Button type="button" size="sm" variant="outline" disabled={remove.isPending} onClick={() => remove.mutate(feature.id)}>Delete {feature.name}</Button> : null}
      </div>
    </li>
  )
}

export function FeatureSets({ siteId, search, timezone, canEdit }: { siteId: number; search: AppSearch; timezone: string; canEdit: boolean }) {
  const features = useFeatureSets(siteId)
  const create = useCreateFeatureSet(siteId)
  const [name, setName] = useState('')
  const [names, setNames] = useState('')
  const [error, setError] = useState<string | null>(null)

  function submit(event: React.FormEvent) {
    event.preventDefault()
    const trimmed = name.trim()
    const parsed = parseFeatureNames(names)
    const problem = trimmed === '' ? 'Enter a name.' : trimmed.length > 80 ? 'The name is at most 80 characters.' : parsed.error
    setError(problem)
    if (problem) return
    create.mutate({ name: trimmed, event_names: parsed.names }, {
      onSuccess: () => { setName(''); setNames('') },
      onError: (failure) => setError(failure.message),
    })
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Feature adoption <InfoTip label="About feature adoption">Group 1 to 20 product events into a feature and see the share of active installs that used it.</InfoTip></CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {features.isError ? <p role="alert" className="text-sm text-destructive">{features.error.message}</p> : null}
        {features.data && features.data.features.length === 0 ? <p className="text-sm text-muted-foreground">No feature sets yet.</p> : null}
        <ul className="flex flex-col gap-2">
          {(features.data?.features ?? []).map((feature) => <FeatureRow key={feature.id} siteId={siteId} feature={feature} search={search} timezone={timezone} canEdit={canEdit} />)}
        </ul>
        {canEdit ? (
          <form className="flex flex-col gap-3" onSubmit={submit} noValidate>
            <Field>
              <FieldLabel htmlFor="feature-name">Feature name</FieldLabel>
              <Input id="feature-name" value={name} maxLength={80} onChange={(event) => setName(event.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="feature-events">Event names</FieldLabel>
              <Textarea id="feature-events" value={names} rows={3} onChange={(event) => setNames(event.target.value)} />
              <FieldDescription>One per line or comma separated, 1 to 20.</FieldDescription>
            </Field>
            {error ? <p role="alert" className="text-sm text-destructive">{error}</p> : null}
            <div><Button type="submit" disabled={create.isPending}>Create feature set</Button></div>
          </form>
        ) : null}
      </CardContent>
    </Card>
  )
}
