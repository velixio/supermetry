import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { FieldDescription, FieldError } from '@/components/ui/field'
import { privateRequest } from '@/features/settings/privateApi'
import type { components } from '@/lib/api.gen'

import { useChannels } from './notificationApi'

type Settings = components['schemas']['AppIssueWebhooks']
type Kind = Settings['kinds'][number]

const KINDS: { kind: Kind; label: string }[] = [
  { kind: 'created', label: 'New issue' },
  { kind: 'resolved', label: 'Resolved' },
  { kind: 'regressed', label: 'Regressed' },
  { kind: 'assigned', label: 'Assigned' },
  { kind: 'escalated', label: 'Escalated' },
  { kind: 'github_linked', label: 'GitHub issue linked' },
]

/** Sends issue events to existing notification channels; webhook channels are signed like alert webhooks. */
export function IssueWebhooks({ orgId, siteId, canEdit }: { orgId: number; siteId: number; canEdit: boolean }) {
  const client = useQueryClient()
  const path = `/api/private/sites/${siteId}/app/issue-webhooks`
  const settings = useQuery({ queryKey: ['app-issue-webhooks', siteId], queryFn: () => privateRequest<Settings>('GET', path, { orgId }) })
  const channels = useChannels(orgId)
  const [chosen, setChosen] = useState<number[]>([])
  const [kinds, setKinds] = useState<Kind[]>([])
  useEffect(() => {
    if (settings.data) {
      setChosen(settings.data.channel_ids ?? [])
      setKinds(settings.data.kinds ?? [])
    }
  }, [settings.data])
  const save = useMutation({
    mutationFn: () => privateRequest<Settings>('PUT', path, { orgId, body: { channel_ids: chosen, kinds } }),
    onSuccess: (value) => client.setQueryData(['app-issue-webhooks', siteId], value),
  })
  const toggle = <T,>(list: T[], value: T) => (list.includes(value) ? list.filter((v) => v !== value) : [...list, value])
  const list = channels.data?.data ?? []
  return (
    <section className="flex flex-col gap-3 rounded-lg border bg-card p-4" aria-label="Issue webhooks">
      <h3 className="text-sm font-medium">Issue webhooks</h3>
      <FieldDescription>Send issue events to notification channels you already set up. Webhook channels get a signed JSON body; email, chat and Telegram channels get one line of text.</FieldDescription>
      <fieldset className="flex flex-col gap-1" disabled={!canEdit}>
        <legend className="text-sm font-medium">Channels</legend>
        {list.length === 0 ? <p className="text-sm text-muted-foreground">No notification channels yet. Add one under Notifications.</p> : null}
        {list.map((channel) => (
          <label key={channel.id} className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={chosen.includes(channel.id)} onChange={() => setChosen(toggle(chosen, channel.id))} />
            {channel.name} <span className="text-muted-foreground">({channel.kind})</span>
          </label>
        ))}
      </fieldset>
      <fieldset className="flex flex-col gap-1" disabled={!canEdit}>
        <legend className="text-sm font-medium">Events</legend>
        {KINDS.map(({ kind, label }) => (
          <label key={kind} className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={kinds.includes(kind)} onChange={() => setKinds(toggle(kinds, kind))} />
            {label}
          </label>
        ))}
      </fieldset>
      {save.error ? <FieldError>{save.error.message}</FieldError> : null}
      {save.isSuccess ? <p role="status" className="text-sm text-muted-foreground">Saved.</p> : null}
      {canEdit ? <div><Button type="button" disabled={save.isPending} onClick={() => save.mutate()}>Save webhooks</Button></div> : null}
    </section>
  )
}
