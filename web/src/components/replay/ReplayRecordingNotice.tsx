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

  return <aside className="space-y-2 rounded-xl border bg-card p-4 text-sm" aria-label="Recording status">
    {pending ? <p role="status">Loading recording settings…</p> : error ? <div role="alert" className="space-y-2">
      <p className="font-medium">Recording settings could not be loaded.</p>
      <p className="text-muted-foreground">{error.message}</p>
      <Button variant="outline" size="sm" onClick={onRetry}>Retry recording settings</Button>
    </div> : settings ? <>
      <h2 className="font-medium">{settings.enabled ? 'Recording is on' : 'Recording is off'}</h2>
      <p className="text-muted-foreground">Instance permission: {settings.instance_enabled === undefined ? 'unavailable' : settings.instance_enabled ? 'on' : 'off'}. Project opt-in: {settings.configured_enabled === undefined ? 'unavailable' : settings.configured_enabled ? 'on' : 'off'}.</p>
      {settings.restrictions?.includes('instance_replay_disabled') || settings.instance_enabled === false ? <p>The instance does not permit new recording. An instance administrator must update the configuration and restart the server.</p> : null}
      {settings.restrictions?.includes('project_replay_disabled') || settings.configured_enabled === false ? <p>This project has not opted in to recording. An owner or admin can enable it in Replay settings when the instance permits it.</p> : null}
      {settings.enabled ? <p className="text-muted-foreground">New recordings follow the effective sampling and consent settings.</p> : null}
    </> : <p role="status">Recording settings are unavailable.</p>}
    <p className="text-muted-foreground">Existing recordings remain available to authorized viewers.</p>
    <a className="inline-block font-medium underline underline-offset-4" href={`/app/${encodeURIComponent(org)}/${encodeURIComponent(site)}/settings/replay`}>Replay settings</a>
  </aside>
}
