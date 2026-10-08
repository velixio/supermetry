import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckIcon, CopyIcon } from 'lucide-react'

import type { Role } from '@/components/Sidebar'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
  FieldTitle,
} from '@/components/ui/field'
import { Switch } from '@/components/ui/switch'
import { getSite, updateSite } from '@/features/sites/api'
import type { TrackerSettings as TrackerSettingsValue } from '@/features/sites/types'
import { copyText } from '@/lib/clipboard'

type TrackerKey = keyof TrackerSettingsValue

const controls: Array<{ key: TrackerKey; label: string; help: string }> = [
  { key: 'outbound', label: 'Track outbound links', help: 'Records clicks that leave your site.' },
  { key: 'downloads', label: 'Track file downloads', help: 'Records clicks on common downloadable file types.' },
  { key: 'forms', label: 'Track form submissions', help: 'Records successful form submissions as custom events.' },
  { key: 'vitals', label: 'Track Web Vitals', help: 'Measures LCP, CLS, and INP when a visitor leaves.' },
  { key: 'not_found', label: 'Track 404 pages', help: 'Records visits to pages marked with the Panel 404 tag.' },
  { key: 'hash', label: 'Track hash routes', help: 'Treats URL hash changes as navigation in hash-based apps.' },
  { key: 'clicks', label: 'Track heatmap clicks', help: 'Clicks follow Heatmaps unless changed' },
]

function changedFields(
  original: TrackerSettingsValue,
  current: TrackerSettingsValue,
): Partial<TrackerSettingsValue> {
  const changed: Partial<TrackerSettingsValue> = {}
  for (const { key } of controls) {
    if (current[key] !== original[key]) changed[key] = current[key]
  }
  return changed
}

export function TrackingSettings({
  orgId,
  role,
  siteId,
}: {
  orgId: number
  role: Role
  siteId: number
}) {
  const canManage = role === 'owner' || role === 'admin'
  if (!canManage) return null
  return <TrackingSettingsForm orgId={orgId} siteId={siteId} />
}

function TrackingSettingsForm({ orgId, siteId }: { orgId: number; siteId: number }) {
  const queryClient = useQueryClient()
  const [settings, setSettings] = useState<TrackerSettingsValue | null>(null)
  const [savedSettings, setSavedSettings] = useState<TrackerSettingsValue | null>(null)
  const [copied, setCopied] = useState<'yes' | 'manual' | null>(null)
  const site = useQuery({
    queryKey: ['site', siteId],
    queryFn: () => getSite(siteId, orgId),
  })

  useEffect(() => {
    if (!site.data?.tracker) return
    setSettings(site.data.tracker)
    setSavedSettings(site.data.tracker)
  }, [site.data])

  const changes = useMemo(() => {
    return settings && savedSettings ? changedFields(savedSettings, settings) : {}
  }, [savedSettings, settings])
  const dirty = Object.keys(changes).length > 0
  const save = useMutation({
    mutationFn: (tracker: Partial<TrackerSettingsValue>) =>
      updateSite(siteId, orgId, { tracker }),
    onSuccess: (value) => {
      queryClient.setQueryData(['site', siteId], value)
      if (value.tracker) {
        setSettings(value.tracker)
        setSavedSettings(value.tracker)
      }
    },
  })

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (dirty) save.mutate(changes)
  }

  async function copyURL() {
    if (!site.data?.tracker_url) return
    setCopied((await copyText(site.data.tracker_url)) ? 'yes' : 'manual')
  }

  return (
    <section
      aria-labelledby="tracking-settings-heading"
      className="flex flex-col gap-5 rounded-xl border bg-card p-5 text-card-foreground"
    >
      <header className="flex flex-col gap-1">
        <h2 id="tracking-settings-heading" className="text-lg font-semibold">
          Tracking
        </h2>
        <p className="text-sm text-muted-foreground">
          Choose which browser events this site's generated script collects. Snippet attributes override these settings.
        </p>
      </header>
      {settings && savedSettings ? (
        <form className="flex flex-col gap-5" onSubmit={submit}>
          <FieldSet>
            <FieldLegend variant="label">Tracker features</FieldLegend>
            <FieldGroup className="gap-4">
              {controls.map((control) => (
                <Field key={control.key} orientation="horizontal">
                  <FieldContent>
                    <FieldLabel htmlFor={`tracker-${control.key}`}>{control.label}</FieldLabel>
                    <FieldDescription>{control.help}</FieldDescription>
                  </FieldContent>
                  <Switch
                    id={`tracker-${control.key}`}
                    checked={settings[control.key]}
                    disabled={save.isPending}
                    onCheckedChange={(checked) =>
                      setSettings((current) =>
                        current ? { ...current, [control.key]: checked } : current,
                      )
                    }
                  />
                </Field>
              ))}
            </FieldGroup>
          </FieldSet>
          <Field>
            <FieldTitle>Script URL</FieldTitle>
            <div className="flex flex-col items-start gap-3 sm:flex-row sm:items-center">
              <code className="max-w-full overflow-x-auto rounded-lg bg-muted px-3 py-2 text-xs select-all">
                {site.data?.tracker_url}
              </code>
              <Button type="button" variant="outline" onClick={() => void copyURL()}>
                {copied === 'yes' ? (
                  <CheckIcon data-icon="inline-start" />
                ) : (
                  <CopyIcon data-icon="inline-start" />
                )}
                Copy script URL
              </Button>
            </div>
            {copied ? (
              <FieldDescription role="status">
                {copied === 'yes' ? 'Copied' : 'Select the URL and press Ctrl+C to copy'}
              </FieldDescription>
            ) : null}
          </Field>
          <FieldDescription>
            Turning every extension off serves the 1 KB core script, without custom-event tags
          </FieldDescription>
          <FieldDescription>Changes can take up to 60 seconds to reach cached scripts.</FieldDescription>
          <FieldError>{save.error instanceof Error ? save.error.message : null}</FieldError>
          <Button className="self-start" type="submit" disabled={!dirty || save.isPending}>
            {save.isPending ? 'Saving…' : 'Save tracking settings'}
          </Button>
        </form>
      ) : site.isError ? (
        <FieldError>
          {site.error instanceof Error
            ? site.error.message
            : 'Tracking settings could not be loaded'}
        </FieldError>
      ) : (
        <p className="text-sm text-muted-foreground">Loading tracking settings…</p>
      )}
    </section>
  )
}
