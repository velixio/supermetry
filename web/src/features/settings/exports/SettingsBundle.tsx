import { useRef, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Field, FieldLabel } from '@/components/ui/field'
import { privateRequest } from '@/features/settings/privateApi'
import { useReauth } from '@/lib/useReauth'
import { uiSettings } from '@/messages/uiSettings'

export type ImportReport = {
  dry_run: boolean
  sites: { name?: string; slug?: string; action?: string; goals?: number; funnels?: number; segments?: number; alerts?: number; error?: string }[]
  members: { email?: string; role?: string; action?: string; note?: string }[]
  warnings: string[]
}

function readText(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(reader.error)
    reader.readAsText(file)
  })
}

export function SettingsBundle({ orgId }: { orgId: number }) {
  const t = uiSettings.exports.bundle
  const { withReauth, reauthDialog } = useReauth()
  const input = useRef<HTMLInputElement>(null)
  const [bundle, setBundle] = useState<Record<string, unknown> | null>(null)
  const [invite, setInvite] = useState(false)
  const [report, setReport] = useState<ImportReport | null>(null)
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const path = (dryRun: boolean, inviteMembers: boolean) => `/api/private/orgs/${orgId}/settings-import?dry_run=${dryRun}&invite_members=${inviteMembers}`

  async function check(next: Record<string, unknown>, inviteMembers: boolean) {
    setBusy(true)
    setError('')
    setMessage('')
    try {
      setReport(await withReauth(() => privateRequest<ImportReport>('POST', path(true, inviteMembers), { orgId, body: next })))
    } catch (failure) {
      setReport(null)
      setError(failure instanceof Error ? failure.message : String(failure))
    } finally {
      setBusy(false)
    }
  }

  async function onFile(file: File | undefined) {
    if (!file) return
    setReport(null)
    setMessage('')
    try {
      const parsed: unknown = JSON.parse(await readText(file))
      if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('not an object')
      setBundle(parsed as Record<string, unknown>)
      await check(parsed as Record<string, unknown>, invite)
    } catch {
      setBundle(null)
      setError(t.invalid)
    }
  }

  async function apply() {
    if (!bundle) return
    setBusy(true)
    setError('')
    try {
      await withReauth(() => privateRequest<ImportReport>('POST', path(false, invite), { orgId, body: bundle }))
      setMessage(t.applied)
      discard()
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure))
    } finally {
      setBusy(false)
    }
  }

  function discard() {
    setBundle(null)
    setReport(null)
    if (input.current) input.current.value = ''
  }

  const nothing = report && report.sites.length === 0 && report.members.length === 0

  return (
    <section aria-labelledby="bundle-title" className="flex flex-col gap-4">
      <div>
        <h2 id="bundle-title" className="text-base font-semibold">{t.title}</h2>
        <p className="text-sm text-muted-foreground">{t.description}</p>
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <Button asChild variant="outline">
          <a href={`/api/private/orgs/${orgId}/settings-export`} download>{t.download}</a>
        </Button>
        <Field className="w-auto">
          <FieldLabel htmlFor="settings-bundle-file" className="sr-only">{t.importLabel}</FieldLabel>
          <input
            ref={input}
            id="settings-bundle-file"
            type="file"
            accept="application/json,.json"
            aria-label={t.importLabel}
            className="text-sm file:mr-3 file:rounded-md file:border file:bg-background file:px-3 file:py-1.5 file:text-sm"
            onChange={(event) => void onFile(event.target.files?.[0])}
          />
        </Field>
      </div>
      <Field orientation="horizontal">
        <Checkbox
          id="settings-bundle-invite"
          checked={invite}
          onCheckedChange={(checked) => {
            const next = checked === true
            setInvite(next)
            if (bundle) void check(bundle, next)
          }}
        />
        <FieldLabel htmlFor="settings-bundle-invite">{t.invite}</FieldLabel>
      </Field>
      {busy && !report ? <p role="status" className="text-sm text-muted-foreground">{t.checking}</p> : null}
      {error ? <p role="alert" className="text-sm text-destructive">{error}</p> : null}
      {message ? <p role="status" className="text-sm text-success">{message}</p> : null}
      {report ? (
        <div className="flex flex-col gap-3 rounded-lg bg-muted/40 p-4" aria-label={t.reportTitle} role="region">
          <h3 className="text-sm font-medium">{t.reportTitle}</h3>
          {nothing ? <p className="text-sm text-muted-foreground">{t.noChanges}</p> : null}
          {report.sites.length ? (
            <div>
              <h4 className="text-xs font-medium text-muted-foreground">{t.sitesHeading}</h4>
              <ul className="text-sm">
                {report.sites.map((site, index) => (
                  <li key={`${site.slug ?? site.name}-${index}`}>
                    {site.name ?? site.slug}: {site.action}
                    {site.goals ? `, ${site.goals} goals` : ''}{site.funnels ? `, ${site.funnels} funnels` : ''}{site.segments ? `, ${site.segments} segments` : ''}{site.alerts ? `, ${site.alerts} alerts` : ''}
                    {site.error ? ` (${site.error})` : ''}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {report.members.length ? (
            <div>
              <h4 className="text-xs font-medium text-muted-foreground">{t.membersHeading}</h4>
              <ul className="text-sm">
                {report.members.map((member, index) => <li key={`${member.email}-${index}`}>{member.email} ({member.role}): {member.action}{member.note ? `, ${member.note}` : ''}</li>)}
              </ul>
            </div>
          ) : null}
          {report.warnings.length ? (
            <div>
              <h4 className="text-xs font-medium text-muted-foreground">{t.warningsHeading}</h4>
              <ul className="list-disc pl-5 text-sm">{report.warnings.map((warning) => <li key={warning}>{warning}</li>)}</ul>
            </div>
          ) : null}
          <div className="flex gap-2">
            <Button type="button" disabled={busy} aria-busy={busy} onClick={() => void apply()}>{busy ? t.applying : t.apply}</Button>
            <Button type="button" variant="outline" disabled={busy} onClick={discard}>{t.discard}</Button>
          </div>
        </div>
      ) : null}
      {reauthDialog}
    </section>
  )
}
