import { useEffect, useMemo, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { en } from '@/messages/en'
import type { ChannelKind } from './notificationApi'
import {
  previewNotificationTemplate, useNotificationTemplates, useResetNotificationTemplate,
  useSaveNotificationSettings, useSaveNotificationTemplate,
} from './notificationTemplatesApi'

const kinds: ChannelKind[] = ['email', 'slack', 'discord', 'telegram', 'webhook']

export function NotificationTemplates({ orgId, canManage }: { orgId: number; canManage: boolean }) {
  const templates = useNotificationTemplates(orgId)
  const save = useSaveNotificationTemplate(orgId)
  const reset = useResetNotificationTemplate(orgId)
  const saveSettings = useSaveNotificationSettings(orgId)
  const [kind, setKind] = useState<ChannelKind>('email')
  const [subject, setSubject] = useState('')
  const [body, setBody] = useState('')
  const [preview, setPreview] = useState<{ subject?: string; body: string } | null>(null)
  const [previewError, setPreviewError] = useState('')
  const template = useMemo(() => templates.data?.data.find((item) => item.kind === kind && typeof item.body === 'string'), [kind, templates.data])

  useEffect(() => {
    if (!template) return
    setSubject(template.subject ?? '')
    setBody(template.body)
  }, [template])

  useEffect(() => {
    if (!body) return
    let active = true
    const timer = window.setTimeout(() => {
      void previewNotificationTemplate(orgId, kind, { subject: kind === 'email' ? subject : undefined, body })
        .then((result) => { if (active) { setPreview(result); setPreviewError('') } })
        .catch((error: Error) => { if (active) { setPreview(null); setPreviewError(error.message) } })
    }, 250)
    return () => { active = false; window.clearTimeout(timer) }
  }, [body, kind, orgId, subject])

  const error = templates.error ?? save.error ?? reset.error ?? saveSettings.error
  return (
    <section aria-labelledby="notification-templates-heading" className="flex flex-col gap-4 border-t pt-6">
      <div className="flex flex-col gap-1">
        <h2 id="notification-templates-heading" className="text-xl font-semibold">{en.notify.templatesTitle}</h2>
        <p className="text-sm text-muted-foreground">{en.notify.templatesDescription}</p>
      </div>
      {error ? <p role="alert" className="text-sm text-destructive">{error.message}</p> : null}
      <label className="flex max-w-xs flex-col gap-1 text-sm font-medium">
        Template channel
        <select className="h-11 rounded-lg border border-input bg-background px-3 capitalize" value={kind} onChange={(event) => setKind(event.target.value as ChannelKind)}>
          {kinds.map((value) => <option key={value} value={value}>{value}</option>)}
        </select>
      </label>
      <div className="grid min-w-0 gap-4 lg:grid-cols-2">
        <div className="flex min-w-0 flex-col gap-3 rounded-xl border bg-card p-4">
          {kind === 'email' ? <label className="flex flex-col gap-1 text-sm font-medium">Subject<Input maxLength={200} value={subject} disabled={!canManage} onChange={(event) => setSubject(event.target.value)} /></label> : null}
          <label className="flex flex-col gap-1 text-sm font-medium">Message<Textarea className="min-h-48 font-mono text-sm" maxLength={4096} value={body} disabled={!canManage} onChange={(event) => setBody(event.target.value)} /></label>
          <p className="text-xs text-muted-foreground">{body.length}/4096 characters</p>
          <div className="flex flex-wrap gap-2">
            <Button className="min-h-11" disabled={!canManage || !body || save.isPending} onClick={() => save.mutate({ kind, input: { subject: kind === 'email' ? subject : undefined, body } })}>Save template</Button>
            <Button className="min-h-11" variant="outline" disabled={!canManage || reset.isPending} onClick={() => reset.mutate(kind)}>Reset to default</Button>
          </div>
        </div>
        <div className="flex min-w-0 flex-col gap-4 rounded-xl border bg-muted/30 p-4">
          <div>
            <h3 className="font-medium">{en.notify.preview}</h3>
            <p className="text-xs text-muted-foreground">Rendered with fixed sample alert data.</p>
          </div>
          {previewError ? <p role="alert" className="break-words text-sm text-destructive">{previewError}</p> : null}
          {preview ? <div className="min-w-0 rounded-lg border bg-background p-4 text-sm"><strong className="block break-words">{preview.subject}</strong><p className="mt-2 whitespace-pre-wrap break-words">{preview.body}</p></div> : <p className="text-sm text-muted-foreground">Enter a valid template to see a preview.</p>}
          <div>
            <h3 className="text-sm font-medium">{en.notify.variables}</h3>
            <div className="mt-2 flex flex-wrap gap-2">{(templates.data?.variables ?? []).map((variable) => <code className="rounded bg-background px-2 py-1 text-xs" key={variable}>{`{{.${variable}}}`}</code>)}</div>
          </div>
        </div>
      </div>
      <label className="flex max-w-sm flex-col gap-1 text-sm font-medium">
        Keep delivery history
        <select className="h-11 rounded-lg border border-input bg-background px-3" disabled={!canManage || saveSettings.isPending} value={templates.data?.settings?.history_retention_days ?? 30} onChange={(event) => saveSettings.mutate({ history_retention_days: Number(event.target.value) })}>
          <option value="7">7 days</option><option value="30">30 days</option><option value="90">90 days</option><option value="365">1 year</option>
        </select>
      </label>
    </section>
  )
}
