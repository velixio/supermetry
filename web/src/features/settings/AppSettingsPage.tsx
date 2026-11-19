import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState, type FormEvent } from 'react'

import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription,
  AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { AppRulesSection } from '@/features/settings/AppRulesSection'
import { privateRequest } from '@/features/settings/privateApi'
import { IssueWebhooks } from '@/features/settings/IssueWebhooks'
import { AppKeyDialog } from '@/features/sites/AppKeyDialog'
import { rotateAppIngestKey } from '@/features/sites/api'

type AppSettings = {
  tag_allowlist: string[]
  max_report_age_days: number
  environments: string[]
  filter_extensions: boolean | null
  filter_crawlers: boolean | null
  filter_localhost: boolean | null
  electron_symbols?: boolean | null
  performance_enabled?: boolean | null
  span_retention_days?: number
  log_retention_days?: number
  log_daily_quota?: number | null
  metric_daily_quota?: number | null
  filter_localhost_default: boolean
}
type Erasure = {
  id: number
  status: 'pending' | 'running' | 'done' | 'failed'
  bodies_deleted: number
  events_anonymized: number
  last_error: string | null
}
const ERASURE_POLL_MS = 2000
type IngestKey = { prefix: string; created_at: string; expires_at: string | null }

const toList = (text: string) => [...new Set(text.split(/[,\n]/).map((item) => item.trim()).filter(Boolean))]
const date = (value: string) => new Date(value).toLocaleDateString()

export function AppSettingsPage({ orgId, siteId, publicId, role }: { orgId: number; siteId: number; publicId: string; role?: string }) {
  const client = useQueryClient()
  const base = `/api/private/sites/${siteId}`
  const settings = useQuery({ queryKey: ['app-settings', siteId], queryFn: () => privateRequest<AppSettings>('GET', `${base}/app-settings`, { orgId }) })
  const keys = useQuery({ queryKey: ['app-ingest-keys', siteId], queryFn: () => privateRequest<{ keys: IngestKey[] }>('GET', `${base}/ingest-keys`, { orgId }) })
  const [tags, setTags] = useState('')
  const [age, setAge] = useState('7')
  const [environments, setEnvironments] = useState('')
  const [confirming, setConfirming] = useState(false)
  const [issued, setIssued] = useState('')
  const [extensions, setExtensions] = useState(true)
  const [crawlers, setCrawlers] = useState(true)
  const [localhost, setLocalhost] = useState(false)
  const [electronSymbols, setElectronSymbols] = useState(true)
  const [logDays, setLogDays] = useState('7')
  const [logQuota, setLogQuota] = useState('5000000')
  const [metricQuota, setMetricQuota] = useState('50000000')
  // Until the owner flips the localhost switch the project keeps following the server default.
  const [localhostTouched, setLocalhostTouched] = useState(false)
  const [eraseId, setEraseId] = useState('')
  const [erasing, setErasing] = useState(false)
  const [erasureId, setErasureId] = useState<number | null>(null)
  const canErase = role === 'owner' || role === 'admin'

  function show(value: AppSettings) {
    setTags(value.tag_allowlist.join('\n'))
    setAge(String(value.max_report_age_days))
    setEnvironments(value.environments.join('\n'))
    setExtensions(value.filter_extensions !== false)
    setCrawlers(value.filter_crawlers !== false)
    setLocalhost(value.filter_localhost ?? value.filter_localhost_default)
    setElectronSymbols(value.electron_symbols !== false)
    setLogDays(String(value.log_retention_days || 7))
    setLogQuota(String(value.log_daily_quota ?? 5_000_000))
    setMetricQuota(String(value.metric_daily_quota ?? 50_000_000))
    setLocalhostTouched(value.filter_localhost !== null && value.filter_localhost !== undefined)
  }
  useEffect(() => { if (settings.data) show(settings.data) }, [settings.data])

  const save = useMutation({
    mutationFn: () => privateRequest<AppSettings>('PUT', `${base}/app-settings`, {
      orgId,
      body: {
        tag_allowlist: toList(tags), max_report_age_days: Number(age), environments: toList(environments),
        filter_extensions: extensions, filter_crawlers: crawlers, filter_localhost: localhostTouched ? localhost : null, electron_symbols: electronSymbols,
        // The server resets any field left out, so send back what this form does not edit.
        performance_enabled: settings.data?.performance_enabled ?? null, span_retention_days: settings.data?.span_retention_days ?? 0,
        log_retention_days: Number(logDays), log_daily_quota: Number(logQuota), metric_daily_quota: Number(metricQuota),
      },
    }),
    onSuccess: (value) => { show(value); client.setQueryData(['app-settings', siteId], value) },
  })
  const rotate = useMutation({
    mutationFn: () => rotateAppIngestKey(siteId, orgId),
    onSuccess: (result) => {
      setConfirming(false)
      setIssued(result.key)
      void client.invalidateQueries({ queryKey: ['app-ingest-keys', siteId] })
    },
  })

  const erase = useMutation({
    mutationFn: () => privateRequest<Erasure>('POST', `${base}/app/users/erase`, { orgId, body: { user_id: eraseId.trim() } }),
    onSuccess: (value) => {
      client.setQueryData(['app-erasure', siteId, value.id], value)
      setErasureId(value.id)
    },
    onSettled: () => setErasing(false),
  })
  // The server erases in the background; poll until the request settles.
  const erasure = useQuery({
    queryKey: ['app-erasure', siteId, erasureId],
    enabled: erasureId !== null,
    queryFn: () => privateRequest<Erasure>('GET', `${base}/app/users/erasures/${erasureId}`, { orgId }),
    refetchInterval: (query) => {
      const status = query.state.data?.status
      return status === 'done' || status === 'failed' ? false : ERASURE_POLL_MS
    },
  })
  const progress = erasureId !== null ? erasure.data : undefined
  const working = erase.isPending || progress?.status === 'pending' || progress?.status === 'running'

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    save.mutate()
  }

  return (
    <div className="flex flex-col gap-6">
      <form className="flex flex-col gap-4 rounded-lg bg-card p-4" onSubmit={submit}>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="app-tags">Tag allowlist</FieldLabel>
            <Textarea id="app-tags" value={tags} onChange={(event) => setTags(event.target.value)} />
            <FieldDescription>Comma or line separated. Reports may only carry these tags.</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="app-age">Max report age (days)</FieldLabel>
            <Input id="app-age" type="number" min={1} max={30} value={age} onChange={(event) => setAge(event.target.value)} />
          </Field>
          <Field>
            <FieldLabel htmlFor="app-environments">Environments</FieldLabel>
            <Textarea id="app-environments" value={environments} onChange={(event) => setEnvironments(event.target.value)} />
            <FieldDescription>Comma or line separated, for example production, staging.</FieldDescription>
          </Field>
          <Field>
            <FieldLabel>Inbound filters</FieldLabel>
            <FieldDescription>Events matching an enabled filter are dropped before they are stored.</FieldDescription>
            <FilterSwitch id="filter-extensions" label="Browser extensions" checked={extensions} onChange={setExtensions}
              hint="Errors raised by code from a browser extension." />
            <FilterSwitch id="filter-crawlers" label="Crawlers" checked={crawlers} onChange={setCrawlers}
              hint="Events sent by search engine and other bots." />
            <FilterSwitch id="filter-localhost" label="Localhost" checked={localhost}
              onChange={(value) => { setLocalhost(value); setLocalhostTouched(true) }}
              hint={`Events raised on a developer machine. Server default: ${settings.data?.filter_localhost_default ? 'on' : 'off'}.`} />
          </Field>
          <Field>
            <FieldLabel>Native crashes</FieldLabel>
            <FilterSwitch id="electron-symbols" label="Fetch Electron's public symbols" checked={electronSymbols} onChange={setElectronSymbols}
              hint="Sends only Electron module names and ids to symbols.electronjs.org." />
          </Field>
          {canErase ? (
            <Field>
              <FieldLabel>Logs and metrics</FieldLabel>
              <FieldDescription>Raw logs are dropped after the retention period. Over a daily quota, new records are rejected until the next UTC day; 0 means no limit.</FieldDescription>
              <FieldLabel htmlFor="app-log-days">Log retention (days)</FieldLabel>
              <Input id="app-log-days" type="number" min={1} max={30} value={logDays} onChange={(event) => setLogDays(event.target.value)} />
              <FieldLabel htmlFor="app-log-quota">Log records per day</FieldLabel>
              <Input id="app-log-quota" type="number" min={0} value={logQuota} onChange={(event) => setLogQuota(event.target.value)} />
              <FieldLabel htmlFor="app-metric-quota">Metric points per day</FieldLabel>
              <Input id="app-metric-quota" type="number" min={0} value={metricQuota} onChange={(event) => setMetricQuota(event.target.value)} />
            </Field>
          ) : null}
          {save.error ? <FieldError>{save.error.message}</FieldError> : null}
        </FieldGroup>
        <div><Button type="submit" disabled={save.isPending}>Save</Button></div>
      </form>

      <IssueWebhooks orgId={orgId} siteId={siteId} canEdit={role === 'owner' || role === 'admin' || role === 'editor'} />

      <section className="flex flex-col gap-3 rounded-lg bg-card p-4" aria-label="Ingest keys">
        <h3 className="text-sm font-medium">Ingest keys</h3>
        <ul className="text-sm">
          {(keys.data?.keys ?? []).map((key) => (
            <li key={key.prefix} className="flex flex-wrap items-center justify-between gap-2 py-2">
              <code>{`smk_${key.prefix}…`}</code>
              <span className="text-muted-foreground">
                Created {date(key.created_at)}{key.expires_at ? `, valid until ${date(key.expires_at)}` : ''}
              </span>
            </li>
          ))}
        </ul>
        {confirming ? (
          <div className="flex flex-wrap items-center gap-2">
            <p className="text-sm">The current key keeps working for 7 days.</p>
            <Button type="button" disabled={rotate.isPending} onClick={() => rotate.mutate()}>Rotate</Button>
            <Button type="button" variant="outline" onClick={() => setConfirming(false)}>Cancel</Button>
          </div>
        ) : (
          <div><Button type="button" variant="outline" onClick={() => setConfirming(true)}>Rotate key</Button></div>
        )}
        {rotate.error ? <p role="alert" className="text-sm text-destructive">{rotate.error.message}</p> : null}
      </section>
      {canErase ? (
        <section className="flex flex-col gap-3 rounded-lg bg-card p-4" aria-label="Erase a user's data">
          <h3 className="text-sm font-medium">Erase a user&apos;s data</h3>
          <p className="text-sm text-muted-foreground">
            Deletes the stored event bodies of this user&apos;s events and clears the user hash on their event rows.
            Aggregate counts stay, without any link to the user.
          </p>
          <Field>
            <FieldLabel htmlFor="erase-user-id">User id</FieldLabel>
            <Input id="erase-user-id" value={eraseId} maxLength={512} autoComplete="off"
              onChange={(event) => { setEraseId(event.target.value); erase.reset(); setErasureId(null) }} />
            <FieldDescription>The user id your SDK reports with <code>setUser</code>.</FieldDescription>
          </Field>
          <div>
            <Button type="button" variant="destructive" disabled={!eraseId.trim() || working} onClick={() => setErasing(true)}>
              Erase data
            </Button>
          </div>
          {erase.error ? <p role="alert" className="text-sm text-destructive">{erase.error.message}</p> : null}
          {erasure.error ? <p role="alert" className="text-sm text-destructive">{erasure.error.message}</p> : null}
          {progress?.status === 'failed' ? (
            <p role="alert" className="text-sm text-destructive">Erasure failed{progress.last_error ? `: ${progress.last_error}` : ''}. Run it again to retry.</p>
          ) : null}
          {progress && (progress.status === 'pending' || progress.status === 'running') ? (
            <p role="status" className="text-sm">
              Erasing in the background. {progress.bodies_deleted} stored event {progress.bodies_deleted === 1 ? 'body' : 'bodies'} deleted
              and {progress.events_anonymized} {progress.events_anonymized === 1 ? 'event' : 'events'} anonymized so far.
            </p>
          ) : null}
          {progress?.status === 'done' ? (
            <p role="status" className="text-sm">
              Erased {progress.bodies_deleted} stored event {progress.bodies_deleted === 1 ? 'body' : 'bodies'} and
              anonymized {progress.events_anonymized} {progress.events_anonymized === 1 ? 'event' : 'events'}.
            </p>
          ) : null}
          <AlertDialog open={erasing} onOpenChange={setErasing}>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>Erase this user&apos;s data?</AlertDialogTitle>
                <AlertDialogDescription>
                  This permanently deletes the stored event bodies of this user&apos;s events. It cannot be undone.
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>Cancel</AlertDialogCancel>
                <AlertDialogAction variant="destructive" onClick={() => erase.mutate()}>Erase</AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </section>
      ) : null}
      <AppRulesSection orgId={orgId} siteId={siteId} canEdit={role === 'owner' || role === 'admin' || role === 'editor'} />
      {issued ? <AppKeyDialog appId={publicId} ingestKey={issued} onClose={() => setIssued('')} /> : null}
    </div>
  )
}

function FilterSwitch({ id, label, hint, checked, onChange }: {
  id: string; label: string; hint: string; checked: boolean; onChange: (value: boolean) => void
}) {
  return (
    <div className="flex items-center justify-between gap-4 py-1">
      <div className="flex flex-col">
        <label className="text-sm font-medium" htmlFor={id}>{label}</label>
        <span className="text-sm text-muted-foreground">{hint}</span>
      </div>
      <Switch id={id} checked={checked} onCheckedChange={onChange} />
    </div>
  )
}
