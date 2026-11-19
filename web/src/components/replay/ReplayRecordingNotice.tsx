import { InfoIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import type { SiteSettings } from '@/lib/replay-api'

// Recording permission is independent of authorization to read retained replays.
export function ReplayRecordingNotice({ settings, pending, error, onRetry, org, site }: {
  settings?: SiteSettings
  pending: boolean
  error?: Error | null
  onRetry: () => void
  org: string
  site: string
}) {
  if (!pending && !error && settings?.enabled) return null

  const strip = 'flex h-9 items-center gap-2 rounded-md bg-muted/50 px-3 text-sm'
  const settingsLink = <a className="shrink-0 font-medium underline underline-offset-4" href={`/app/${encodeURIComponent(org)}/${encodeURIComponent(site)}/settings/replay`}>Replay settings</a>

  if (pending) return <aside className={strip} aria-label="Recording status"><p role="status" className="min-w-0 flex-1">Loading recording settings…</p>{settingsLink}</aside>
  if (error) return <aside className={strip} aria-label="Recording status">
    <div role="alert" className="flex min-w-0 flex-1 items-center gap-2">
      <p className="shrink-0 font-medium">Recording settings could not be loaded.</p>
      <p className="min-w-0 truncate text-muted-foreground" title={error.message}>{error.message}</p>
      <Button className="ml-auto h-7" variant="secondary" size="sm" onClick={onRetry}>Retry recording settings</Button>
    </div>
    {settingsLink}
  </aside>
  if (!settings) return <aside className={strip} aria-label="Recording status"><p role="status" className="min-w-0 flex-1 truncate">Recording settings are unavailable.</p>{settingsLink}</aside>

  const instanceOff = settings.restrictions?.includes('instance_replay_disabled') || settings.instance_enabled === false
  const projectOff = settings.restrictions?.includes('project_replay_disabled') || settings.configured_enabled === false
  const instanceText = 'The instance does not permit new recording. An instance administrator must update the configuration and restart the server.'
  const projectText = 'This project has not opted in to recording. An owner or admin can enable it in Replay settings when the instance permits it.'
  // The strip shows only the most relevant sentence; the rest stays in the DOM for assistive tech.
  const primary = instanceOff ? instanceText : projectOff ? projectText : settings.enabled ? 'New recordings follow the effective sampling and consent settings.' : ''
  const extra = instanceOff && projectOff ? projectText : ''
  const permission = `Instance permission: ${settings.instance_enabled === undefined ? 'unavailable' : settings.instance_enabled ? 'on' : 'off'}. Project opt-in: ${settings.configured_enabled === undefined ? 'unavailable' : settings.configured_enabled ? 'on' : 'off'}.`

  return <aside className={strip} aria-label="Recording status">
    <InfoIcon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
    <h2 className="shrink-0 font-medium">{settings.enabled ? 'Recording is on' : 'Recording is off'}</h2>
    {primary ? <p className="min-w-0 flex-1 truncate text-muted-foreground" title={[primary, extra, permission].filter(Boolean).join(' ')}>{primary}</p> : <span className="flex-1" />}
    <div className="sr-only">
      <p>{permission}</p>
      {extra ? <p>{extra}</p> : null}
      <p>Existing recordings remain available to authorized viewers.</p>
    </div>
    {settingsLink}
  </aside>
}
