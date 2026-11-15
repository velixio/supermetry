import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { privateRequest } from '@/features/settings/privateApi'
import { AppKeyDialog } from '@/features/sites/AppKeyDialog'
import { rotateAppIngestKey } from '@/features/sites/api'

type AppSettings = { tag_allowlist: string[]; max_report_age_days: number; environments: string[] }
type IngestKey = { prefix: string; created_at: string; expires_at: string | null }

const toList = (text: string) => [...new Set(text.split(/[,\n]/).map((item) => item.trim()).filter(Boolean))]
const date = (value: string) => new Date(value).toLocaleDateString()

export function AppSettingsPage({ orgId, siteId, publicId }: { orgId: number; siteId: number; publicId: string }) {
  const client = useQueryClient()
  const base = `/api/private/sites/${siteId}`
  const settings = useQuery({ queryKey: ['app-settings', siteId], queryFn: () => privateRequest<AppSettings>('GET', `${base}/app-settings`, { orgId }) })
  const keys = useQuery({ queryKey: ['app-ingest-keys', siteId], queryFn: () => privateRequest<{ keys: IngestKey[] }>('GET', `${base}/ingest-keys`, { orgId }) })
  const [tags, setTags] = useState('')
  const [age, setAge] = useState('7')
  const [environments, setEnvironments] = useState('')
  const [confirming, setConfirming] = useState(false)
  const [issued, setIssued] = useState('')

  function show(value: AppSettings) {
    setTags(value.tag_allowlist.join('\n'))
    setAge(String(value.max_report_age_days))
    setEnvironments(value.environments.join('\n'))
  }
  useEffect(() => { if (settings.data) show(settings.data) }, [settings.data])

  const save = useMutation({
    mutationFn: () => privateRequest<AppSettings>('PUT', `${base}/app-settings`, {
      orgId,
      body: { tag_allowlist: toList(tags), max_report_age_days: Number(age), environments: toList(environments) },
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

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    save.mutate()
  }

  return (
    <div className="flex flex-col gap-6">
      <form className="flex flex-col gap-4 rounded-xl border bg-card p-4" onSubmit={submit}>
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
          {save.error ? <FieldError>{save.error.message}</FieldError> : null}
        </FieldGroup>
        <div><Button type="submit" disabled={save.isPending}>Save</Button></div>
      </form>

      <section className="flex flex-col gap-3 rounded-xl border bg-card p-4" aria-label="Ingest keys">
        <h3 className="text-sm font-medium">Ingest keys</h3>
        <ul className="divide-y text-sm">
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
      {issued ? <AppKeyDialog appId={publicId} ingestKey={issued} onClose={() => setIssued('')} /> : null}
    </div>
  )
}
