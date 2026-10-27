import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

import {
  type OrgNotificationChannel,
  useDeleteOrgTelegram, useOrgNotifications, useSaveOrgTelegram, useTestOrgTelegram,
} from './orgNotificationsApi'

function formatTime(value: string | null) {
  return value ? new Date(value).toLocaleString() : 'never'
}

export function OrgTelegramCard({ orgId }: { orgId: number }) {
  const query = useOrgNotifications(orgId)
  const save = useSaveOrgTelegram(orgId)
  const remove = useDeleteOrgTelegram(orgId)
  const test = useTestOrgTelegram(orgId)
  const channel: OrgNotificationChannel | undefined = query.data?.data.find((item) => item.kind === 'telegram')
  const kinds = query.data?.event_kinds ?? []

  const [token, setToken] = useState('')
  const [chatId, setChatId] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [events, setEvents] = useState<Record<string, boolean>>({})
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    setChatId(channel?.chat_id ?? '')
    setEnabled(channel?.enabled ?? true)
    setEvents(channel?.events ?? {})
    setToken('')
  }, [channel])

  function submit(event: FormEvent) {
    event.preventDefault()
    setSaved(false)
    test.reset()
    save.mutate(
      { ...(token ? { token } : {}), chat_id: chatId.trim(), enabled, events },
      { onSuccess: () => setSaved(true) },
    )
  }

  const error = query.error ?? save.error ?? remove.error ?? test.error

  return (
    <Card>
      <CardHeader>
        <CardTitle>Telegram</CardTitle>
        <CardDescription>
          Send organization events, such as GitHub star milestones, to a Telegram chat. Messages contain
          repository names, counts and links only, never the names of people.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form className="flex flex-col gap-5" onSubmit={submit}>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="org-telegram-token">Telegram bot token</FieldLabel>
              <Input
                id="org-telegram-token"
                type="password"
                autoComplete="new-password"
                required={!channel?.token_set}
                placeholder={channel?.token_set ? 'Saved' : ''}
                value={token}
                onChange={(event) => setToken(event.target.value)}
              />
              <FieldDescription>
                {channel?.token_set
                  ? 'Token saved. Leave blank to keep it, or enter a replacement.'
                  : 'Encrypted when saved and never shown again.'}
              </FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="org-telegram-chat">Telegram chat ID</FieldLabel>
              <Input id="org-telegram-chat" required value={chatId} onChange={(event) => setChatId(event.target.value)} />
              <FieldDescription>A numeric chat ID (groups start with a minus sign) or an @channel username.</FieldDescription>
            </Field>
            <Field orientation="horizontal">
              <Switch id="org-telegram-enabled" checked={enabled} onCheckedChange={setEnabled} />
              <FieldLabel htmlFor="org-telegram-enabled">Send Telegram messages</FieldLabel>
            </Field>
          </FieldGroup>

          <fieldset className="flex flex-col gap-3">
            <legend className="text-sm font-medium">Send a message when</legend>
            {kinds.map((kind) => (
              <div className="flex items-start gap-3" key={kind.kind}>
                <Checkbox
                  id={`org-telegram-event-${kind.kind}`}
                  checked={events[kind.kind] === true}
                  onCheckedChange={(checked) => setEvents((value) => ({ ...value, [kind.kind]: checked === true }))}
                />
                <div className="flex flex-col gap-0.5">
                  <label className="text-sm font-medium" htmlFor={`org-telegram-event-${kind.kind}`}>{kind.label}</label>
                  <span className="text-sm text-muted-foreground">{kind.description}</span>
                </div>
              </div>
            ))}
          </fieldset>

          {channel ? (
            <p className="text-sm text-muted-foreground" role="status">
              Last delivered: {formatTime(channel.last_ok_at)}
              {channel.consecutive_failures > 0 ? ` · ${channel.consecutive_failures} failed in a row` : ''}
              {channel.last_error ? ` · Last error: ${channel.last_error}` : ''}
            </p>
          ) : null}
          {test.data ? (
            <p role="status" className={test.data.ok ? 'text-sm text-muted-foreground' : 'text-sm text-destructive'}>
              {test.data.ok ? 'Test message sent.' : `Test failed: ${test.data.error ?? 'unknown error'}`}
            </p>
          ) : null}
          {saved ? <p role="status" className="text-sm text-muted-foreground">Saved.</p> : null}
          <FieldError>{error?.message}</FieldError>

          <div className="flex flex-wrap gap-2">
            <Button type="submit" disabled={save.isPending}>Save</Button>
            {channel ? (
              <>
                <Button type="button" variant="outline" disabled={test.isPending} onClick={() => test.mutate()}>
                  Send test message
                </Button>
                <Button type="button" variant="outline" disabled={remove.isPending} onClick={() => remove.mutate()}>
                  Remove
                </Button>
              </>
            ) : null}
          </div>

          <details className="text-sm text-muted-foreground">
            <summary className="cursor-pointer font-medium text-foreground">How to set up a bot</summary>
            <ol className="mt-2 list-decimal space-y-1 pl-5">
              <li>In Telegram, open @BotFather, send /newbot and follow the prompts. Copy the token it gives you.</li>
              <li>Add the bot to the chat or channel that should receive messages (as an admin for channels).</li>
              <li>Send any message in that chat, then open https://api.telegram.org/bot&lt;token&gt;/getUpdates in a browser and copy the numeric chat id. Do not share that address, it contains your token.</li>
            </ol>
          </details>
        </form>
      </CardContent>
    </Card>
  )
}
