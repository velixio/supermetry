import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { AppKeyDialog } from '@/features/sites/AppKeyDialog'
import { AppProjectForm } from '@/features/sites/AppProjectForm'
import { rotateAppIngestKey } from '@/features/sites/api'
import type { AppSiteInput, ManagedSite } from '@/features/sites/types'
import type { RetentionPolicy } from '@/lib/instanceConfig'
import { en } from '@/messages/en'

/** Creates the app, then issues its first ingest key and shows it once. `onDone` runs when the dialog closes. */
export function AppCreateFlow({ orgId, createSite, retention, onDone }: {
  orgId: number
  createSite: (input: AppSiteInput) => Promise<ManagedSite>
  retention?: RetentionPolicy
  onDone: (site: ManagedSite) => void
}) {
  const [site, setSite] = useState<ManagedSite | null>(null)
  const [key, setKey] = useState('')
  const [keyError, setKeyError] = useState('')
  const [busy, setBusy] = useState(false)

  async function issueKey(target: ManagedSite) {
    setBusy(true)
    setKeyError('')
    try {
      setKey((await rotateAppIngestKey(target.id, orgId)).key)
    } catch (cause) {
      setKeyError(cause instanceof Error ? cause.message : 'The ingest key could not be created.')
    } finally {
      setBusy(false)
    }
  }

  if (site && !key) {
    return (
      <div className="flex flex-col gap-3">
        <p role="alert" className="text-sm text-destructive">{keyError || 'Creating the ingest key…'}</p>
        <p className="text-sm text-muted-foreground">The app “{site.name}” was created, but it has no ingest key yet.</p>
        <Button type="button" disabled={busy} onClick={() => void issueKey(site)}>Create key</Button>
      </div>
    )
  }

  return (
    <>
      <AppProjectForm
        submitLabel={en.pixels.createApp}
        retention={retention}
        onSubmit={async (input) => {
          const created = await createSite(input)
          setSite(created)
          await issueKey(created)
        }}
      />
      {site && key ? <AppKeyDialog appId={site.public_id} ingestKey={key} siteId={site.id} timezone={site.timezone} onClose={() => onDone(site)} /> : null}
    </>
  )
}
