import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { uiAlerts as t } from '@/messages/uiAlerts'

import {
  type ChannelInput, type ChannelKind, type NotificationChannel,
  useChannels, useDeleteChannel, useSaveChannel, useTestChannel,
} from './notificationApi'
import { NotificationTemplates } from './NotificationTemplates'

const blank: ChannelInput = { kind: 'email', name: '', enabled: true, config: { recipients: [] } }

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
      onSuccess: () => setSent(t.channels.testSent(channel.name)),
    })
  }

  return (
    <PageContainer width="narrow">
      <PageHeader
        title={t.channels.title}
        actions={canManage ? <Button onClick={() => setEditing(null)}>{t.channels.add}</Button> : undefined}
      />
      {channels.error || remove.error || test.error ? (
        <p role="alert" className="text-sm text-destructive">
          {channels.error?.message ?? remove.error?.message ?? test.error?.message}
        </p>
      ) : null}
      {sent ? <p role="status" className="text-sm text-muted-foreground">{sent}</p> : null}
      {channels.data && channels.data.data.length > 0 ? <ul className="flex flex-col rounded-lg bg-card px-4">
        {channels.data.data.map((channel) => (
          <li className="flex flex-wrap items-center justify-between gap-3 py-3" key={channel.id}>
            <div>
              <h2 className="text-sm font-medium">{channel.name}</h2>
              <p className="text-sm capitalize text-muted-foreground">
                {channel.kind}{channel.config.recipients?.length ? ` · ${channel.config.recipients.join(', ')}` : ''}
                {channel.config.chat_id ? ` · ${channel.config.chat_id}` : ''}
                {channel.config.configured ? t.channels.configured : ''} · {channel.enabled ? t.common.enabled : t.common.disabled}
              </p>
            </div>
            {canManage ? (
              <div className="flex flex-wrap gap-2">
                <Button variant="secondary" size="sm" className="max-md:min-h-11" disabled={test.isPending} aria-label={t.channels.sendTest(channel.name)} onClick={() => sendTest(channel)}>
                  {t.channels.test}
                </Button>
                {channel.kind === 'email' || channel.kind === 'telegram' || channel.kind === 'pagerduty' ? <Button variant="secondary" size="sm" className="max-md:min-h-11" onClick={() => setEditing(channel)}>{t.common.edit}</Button> : null}
                <Button variant="secondary" size="sm" className="max-md:min-h-11" disabled={remove.isPending} onClick={() => remove.mutate(channel.id)}>{t.common.delete}</Button>
              </div>
            ) : null}
          </li>
        ))}
      </ul> : null}
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
      <NotificationTemplates orgId={orgId} canManage={canManage} />
    </PageContainer>
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
      enabled: channel.enabled,
      config: channel.kind === 'telegram'
        ? { bot_token: '', chat_id: channel.config.chat_id ?? '' }
        : channel.kind === 'pagerduty'
          ? { routing_key: '' }
          : { recipients: channel.config.recipients ?? [] },
    } : blank)
  }, [channel])

  if (channel === undefined) return null
  const needsURL = input.kind === 'webhook' || input.kind === 'slack' || input.kind === 'discord'
  const isTelegram = input.kind === 'telegram'
  const isPagerDuty = input.kind === 'pagerduty'
  function submit(event: FormEvent) {
    event.preventDefault()
    void onSave(input).catch(() => undefined)
  }
  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <form className="flex flex-col gap-5" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{channel ? t.channels.editTitle : t.channels.addTitle}</DialogTitle>
            <DialogDescription>{t.channels.dialogNote}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="channel-kind">{t.channels.type}</FieldLabel>
              <select id="channel-kind" disabled={Boolean(channel)} className="h-9 rounded-md border border-input bg-background px-2 text-sm" value={input.kind} onChange={(event) => setInput({ kind: event.target.value as ChannelKind, name: input.name, enabled: input.enabled, config: {} })}>
                {(["email", "webhook", "slack", "discord", "telegram", "pagerduty"] as const).map((kind) => <option key={kind} value={kind}>{t.common.channelKind[kind]}</option>)}
              </select>
            </Field>
            <Field>
              <FieldLabel htmlFor="channel-name">{t.channels.name}</FieldLabel>
              <Input id="channel-name" required value={input.name} onChange={(event) => setInput((value) => ({ ...value, name: event.target.value }))} />
            </Field>
            {input.kind === 'email' ? (
              <Field>
                <FieldLabel htmlFor="channel-recipients">{t.channels.recipients}</FieldLabel>
                <Input id="channel-recipients" type="email" multiple required value={input.config.recipients?.join(', ') ?? ''} onChange={(event) => setInput((value) => ({ ...value, config: { recipients: event.target.value.split(',').map((item) => item.trim()).filter(Boolean) } }))} />
                <FieldDescription>{t.channels.recipientsNote}</FieldDescription>
              </Field>
            ) : null}
            {needsURL ? (
              <Field>
                <FieldLabel htmlFor="channel-url">{t.channels.endpoint}</FieldLabel>
                <Input id="channel-url" type="url" required={!channel?.config.configured} value={input.config.url ?? ''} onChange={(event) => setInput((value) => ({ ...value, config: { ...value.config, url: event.target.value } }))} />
                {channel?.config.configured ? <FieldDescription>{t.channels.endpointKeep}</FieldDescription> : null}
              </Field>
            ) : null}
            {input.kind === 'webhook' ? (
              <Field>
                <FieldLabel htmlFor="channel-secret">{t.channels.secret}</FieldLabel>
                <Input id="channel-secret" type="password" value={input.config.secret ?? ''} onChange={(event) => setInput((value) => ({ ...value, config: { ...value.config, secret: event.target.value } }))} />
                <FieldDescription>{t.channels.secretNote}</FieldDescription>
              </Field>
            ) : null}
            {isTelegram ? (
              <>
                <Field>
                  <FieldLabel htmlFor="channel-bot-token">{t.channels.botToken}</FieldLabel>
                  <Input id="channel-bot-token" autoComplete="new-password" required={!channel?.config.configured} type="password" value={input.config.bot_token ?? ''} onChange={(event) => setInput((value) => ({ ...value, config: { ...value.config, bot_token: event.target.value } }))} />
                  <FieldDescription>{channel?.config.configured ? t.channels.botTokenKeep : t.channels.botTokenNew}</FieldDescription>
                </Field>
                <Field>
                  <FieldLabel htmlFor="channel-chat-id">{t.channels.chatId}</FieldLabel>
                  <Input id="channel-chat-id" required value={input.config.chat_id ?? ''} onChange={(event) => setInput((value) => ({ ...value, config: { ...value.config, chat_id: event.target.value } }))} />
                  <FieldDescription>{t.channels.chatIdNote}</FieldDescription>
                </Field>
              </>
            ) : null}
            {isPagerDuty ? (
              <Field>
                <FieldLabel htmlFor="channel-routing-key">{t.channels.routingKey}</FieldLabel>
                <Input id="channel-routing-key" autoComplete="new-password" required={!channel?.config.configured} type="password" value={input.config.routing_key ?? ''} onChange={(event) => setInput((value) => ({ ...value, config: { ...value.config, routing_key: event.target.value } }))} />
                <FieldDescription>{channel?.config.configured ? t.channels.routingKeyKeep : t.channels.routingKeyNew}</FieldDescription>
              </Field>
            ) : null}
            <Field orientation="horizontal">
              <input id="channel-enabled" checked={input.enabled} type="checkbox" onChange={(event) => setInput((value) => ({ ...value, enabled: event.target.checked }))} />
              <FieldLabel htmlFor="channel-enabled">{t.common.enabled}</FieldLabel>
            </Field>
          </FieldGroup>
          <FieldError>{error?.message}</FieldError>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>{t.common.cancel}</Button>
            <Button type="submit" disabled={pending}>{channel ? t.channels.saveChanges : t.channels.create}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
