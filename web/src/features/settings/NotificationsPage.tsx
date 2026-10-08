import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

import {
  type ChannelInput, type ChannelKind, type NotificationChannel,
  useChannels, useDeleteChannel, useSaveChannel, useTestChannel,
} from './notificationApi'

const blank: ChannelInput = { kind: 'email', name: '', config: { recipients: [] } }

export function NotificationsPage({ orgId, canManage = true }: { orgId: number; canManage?: boolean }) {
  const channels = useChannels(orgId)
  const save = useSaveChannel(orgId)
  const remove = useDeleteChannel(orgId)
  const test = useTestChannel(orgId)
  const [editing, setEditing] = useState<NotificationChannel | null | undefined>(undefined)
  const [sent, setSent] = useState('')

  function sendTest(channel: NotificationChannel) {
    setSent('')
    test.mutate(channel.id, {
      onSuccess: () => setSent(`Test notification sent to ${channel.name}.`),
    })
  }

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-6">
      <header className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-1">
          <h1 className="text-2xl font-semibold tracking-tight">Notifications</h1>
          <p className="text-sm text-muted-foreground">Choose where alert notifications are delivered.</p>
        </div>
        {canManage ? <Button onClick={() => setEditing(null)}>Add channel</Button> : null}
      </header>
      {channels.error || remove.error || test.error ? (
        <p role="alert" className="text-sm text-destructive">
          {channels.error?.message ?? remove.error?.message ?? test.error?.message}
        </p>
      ) : null}
      {sent ? <p role="status" className="text-sm text-muted-foreground">{sent}</p> : null}
      <div className="flex flex-col gap-3">
        {channels.data?.data.map((channel) => (
          <section className="flex flex-wrap items-center justify-between gap-3 rounded-xl border bg-card p-4" key={channel.id}>
            <div>
              <h2 className="font-medium">{channel.name}</h2>
              <p className="text-sm capitalize text-muted-foreground">
                {channel.kind}{channel.config.recipients?.length ? ` · ${channel.config.recipients.join(', ')}` : ''}
                {channel.config.configured ? ' · Configured' : ''}
              </p>
            </div>
            {canManage ? (
              <div className="flex flex-wrap gap-2">
                <Button variant="outline" disabled={test.isPending} onClick={() => sendTest(channel)}>
                  Send test to {channel.name}
                </Button>
                {channel.kind === 'email' ? <Button variant="outline" onClick={() => setEditing(channel)}>Edit</Button> : null}
                <Button variant="outline" disabled={remove.isPending} onClick={() => remove.mutate(channel.id)}>Delete</Button>
              </div>
            ) : null}
          </section>
        ))}
      </div>
      <ChannelDialog
        channel={editing}
        error={save.error}
        pending={save.isPending}
        onClose={() => setEditing(undefined)}
        onSave={async (input) => {
          await save.mutateAsync({ id: editing?.id, input })
          setEditing(undefined)
        }}
      />
    </div>
  )
}

function ChannelDialog({ channel, error, pending, onClose, onSave }: {
  channel: NotificationChannel | null | undefined
  error: Error | null
  pending: boolean
  onClose: () => void
  onSave: (input: ChannelInput) => Promise<void>
}) {
  const [input, setInput] = useState<ChannelInput>(blank)
  useEffect(() => {
    setInput(channel ? {
      kind: channel.kind,
      name: channel.name,
      config: { recipients: channel.config.recipients ?? [] },
    } : blank)
  }, [channel])

  if (channel === undefined) return null
  const needsURL = input.kind !== 'email'
  function submit(event: FormEvent) {
    event.preventDefault()
    void onSave(input).catch(() => undefined)
  }
  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <form className="flex flex-col gap-5" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{channel ? 'Edit channel' : 'Add notification channel'}</DialogTitle>
            <DialogDescription>Secrets are stored securely and are never returned by the API.</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="channel-kind">Channel type</FieldLabel>
              <select id="channel-kind" className="h-8 rounded-lg border border-input bg-background px-2 text-sm" value={input.kind} onChange={(event) => setInput({ kind: event.target.value as ChannelKind, name: input.name, config: {} })}>
                <option value="email">Email</option><option value="webhook">Webhook</option><option value="slack">Slack</option><option value="discord">Discord</option>
              </select>
            </Field>
            <Field>
              <FieldLabel htmlFor="channel-name">Name</FieldLabel>
              <Input id="channel-name" required value={input.name} onChange={(event) => setInput((value) => ({ ...value, name: event.target.value }))} />
            </Field>
            {input.kind === 'email' ? (
              <Field>
                <FieldLabel htmlFor="channel-recipients">Recipients</FieldLabel>
                <Input id="channel-recipients" type="email" multiple required value={input.config.recipients?.join(', ') ?? ''} onChange={(event) => setInput((value) => ({ ...value, config: { recipients: event.target.value.split(',').map((item) => item.trim()).filter(Boolean) } }))} />
                <FieldDescription>Separate email addresses with commas.</FieldDescription>
              </Field>
            ) : null}
            {needsURL ? (
              <Field>
                <FieldLabel htmlFor="channel-url">Endpoint URL</FieldLabel>
                <Input id="channel-url" type="url" required={!channel?.config.configured} value={input.config.url ?? ''} onChange={(event) => setInput((value) => ({ ...value, config: { ...value.config, url: event.target.value } }))} />
                {channel?.config.configured ? <FieldDescription>Leave blank to keep the configured endpoint.</FieldDescription> : null}
              </Field>
            ) : null}
            {input.kind === 'webhook' ? (
              <Field>
                <FieldLabel htmlFor="channel-secret">Signing secret</FieldLabel>
                <Input id="channel-secret" type="password" value={input.config.secret ?? ''} onChange={(event) => setInput((value) => ({ ...value, config: { ...value.config, secret: event.target.value } }))} />
                <FieldDescription>Optional. Leave blank when editing to keep the current secret.</FieldDescription>
              </Field>
            ) : null}
          </FieldGroup>
          <FieldError>{error?.message}</FieldError>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
            <Button type="submit" disabled={pending}>{channel ? 'Save changes' : 'Create channel'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
