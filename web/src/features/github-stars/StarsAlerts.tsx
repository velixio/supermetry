import { Link } from '@tanstack/react-router'
import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { FieldError } from '@/components/ui/field'
import { Skeleton } from '@/components/ui/skeleton'
import { useChannels } from '@/features/settings/notificationApi'
import { useSaveStarsAlerts, useStarsAlerts, useTestStarsAlert } from './alertsApi'
import { alertsText } from './alertsMessages'
import { useStarsOrg } from './starsOrg'

/** Which events alert on a repository and which of the organization's channels receive them. */
export function StarsAlerts({ org, repoId }: { org: string; repoId: number }) {
  const { orgId, canManage } = useStarsOrg()
  const settings = useStarsAlerts(repoId)
  const channels = useChannels(orgId)
  const save = useSaveStarsAlerts(repoId)
  const test = useTestStarsAlert(repoId)
  const [events, setEvents] = useState<Record<string, boolean>>({})
  const [selected, setSelected] = useState<number[]>([])
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    if (!settings.data) return
    setEvents(settings.data.events)
    setSelected(settings.data.channel_ids)
  }, [settings.data])

  function toggleChannel(id: number, on: boolean) {
    setSelected((value) => (on ? [...value, id] : value.filter((item) => item !== id)))
  }

  function submit(event: FormEvent) {
    event.preventDefault()
    setSaved(false)
    test.reset()
    save.mutate({ events, channel_ids: selected }, { onSuccess: () => setSaved(true) })
  }

  const list = channels.data?.data ?? []
  const noChannelSelected = Object.values(events).some(Boolean) && selected.length === 0
  const error = settings.error ?? save.error ?? test.error

  return (
    <Card aria-labelledby="stars-alerts-title">
      <CardHeader>
        <CardTitle id="stars-alerts-title">{alertsText.title}</CardTitle>
        <CardDescription>{alertsText.description}</CardDescription>
      </CardHeader>
      <CardContent>
        {settings.isPending ? <Skeleton aria-label={alertsText.loading} className="h-32 w-full" /> : (
          <form className="flex flex-col gap-5" onSubmit={submit}>
            <fieldset className="flex flex-col gap-3" disabled={!canManage}>
              <legend className="text-sm font-medium">{alertsText.events}</legend>
              {(settings.data?.event_kinds ?? []).map((kind) => (
                <div className="flex items-start gap-3" key={kind.kind}>
                  <Checkbox
                    id={`stars-alert-event-${kind.kind}`}
                    checked={events[kind.kind] === true}
                    disabled={!canManage}
                    onCheckedChange={(checked) => setEvents((value) => ({ ...value, [kind.kind]: checked === true }))}
                  />
                  <div className="flex flex-col gap-0.5">
                    <label className="text-sm font-medium" htmlFor={`stars-alert-event-${kind.kind}`}>{kind.label}</label>
                    <span className="text-sm text-muted-foreground">{kind.description}</span>
                  </div>
                </div>
              ))}
            </fieldset>

            <fieldset className="flex flex-col gap-3" disabled={!canManage}>
              <legend className="text-sm font-medium">{alertsText.channels}</legend>
              {list.length === 0 && !channels.isPending ? (
                <p className="text-sm text-muted-foreground">{alertsText.noChannels}</p>
              ) : list.map((channel) => (
                <div className="flex items-center gap-3" key={channel.id}>
                  <Checkbox
                    id={`stars-alert-channel-${channel.id}`}
                    checked={selected.includes(channel.id)}
                    disabled={!canManage}
                    onCheckedChange={(checked) => toggleChannel(channel.id, checked === true)}
                  />
                  <label className="text-sm" htmlFor={`stars-alert-channel-${channel.id}`}>
                    {channel.name}
                    <span className="ml-2 text-muted-foreground capitalize">{channel.kind}{channel.enabled ? '' : ` · ${alertsText.disabled}`}</span>
                  </label>
                </div>
              ))}
              <Link to="/app/$org/settings/notifications" params={{ org }} className="w-fit text-sm underline underline-offset-4">
                {alertsText.manageChannels}
              </Link>
            </fieldset>

            {noChannelSelected ? <p className="text-sm text-muted-foreground">{alertsText.needChannel}</p> : null}
            {test.data ? (
              <ul className="flex flex-col gap-1 text-sm" aria-label={alertsText.testResults}>
                {test.data.data.length === 0 ? <li>{alertsText.testNone}</li> : test.data.data.map((item) => {
                  const name = list.find((channel) => channel.id === item.channel_id)?.name ?? `#${item.channel_id}`
                  return item.ok
                    ? <li key={item.channel_id}>{alertsText.testOk(name)}</li>
                    : <li className="text-destructive" key={item.channel_id}>{alertsText.testFailed(name, item.error ?? '')}</li>
                })}
              </ul>
            ) : null}
            {saved ? <p role="status" className="text-sm text-muted-foreground">{alertsText.saved}</p> : null}
            <FieldError>{error?.message}</FieldError>

            {canManage ? (
              <div className="flex flex-wrap gap-2">
                <Button type="submit" disabled={save.isPending}>{alertsText.save}</Button>
                <Button type="button" variant="outline" disabled={test.isPending || selected.length === 0} onClick={() => test.mutate()}>
                  {alertsText.sendTest}
                </Button>
              </div>
            ) : <p className="text-sm text-muted-foreground">{alertsText.viewOnly}</p>}
          </form>
        )}
      </CardContent>
    </Card>
  )
}
