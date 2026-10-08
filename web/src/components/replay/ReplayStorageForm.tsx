import { useEffect, useState, type FormEvent } from 'react'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import type { ReplayStorageSettings } from '@/lib/replay-api'

function EnvironmentNote({ locked }: { locked: boolean }) {
  return locked ? <FieldDescription>Set by environment variable</FieldDescription> : null
}

export function ReplayStorageForm({
  settings,
  saving,
  onSave,
}: {
  settings: ReplayStorageSettings
  saving: boolean
  onSave: (settings: ReplayStorageSettings, migrate: boolean) => void
}) {
  const [draft, setDraft] = useState(settings)
  const [confirming, setConfirming] = useState(false)
  useEffect(() => setDraft(settings), [settings])

  function submit(event: FormEvent) {
    event.preventDefault()
    setConfirming(true)
  }

  function save(migrate: boolean) {
    onSave(draft, migrate)
    setConfirming(false)
  }

  return (
    <>
      <form onSubmit={submit}>
        <Card>
          <CardHeader>
            <CardTitle>Replay storage</CardTitle>
            <CardDescription>Store replay blobs on local disk or S3-compatible object storage.</CardDescription>
          </CardHeader>
          <CardContent>
            <FieldGroup>
              <Field data-disabled={draft.backend.locked || undefined}>
                <FieldLabel htmlFor="replay-backend">Backend</FieldLabel>
                <select
                  id="replay-backend"
                  className="h-8 rounded-lg border border-input bg-transparent px-2.5 text-sm"
                  disabled={draft.backend.locked}
                  value={draft.backend.value}
                  onChange={(event) => setDraft((current) => ({ ...current, backend: { ...current.backend, value: event.target.value as 'disk' | 's3' } }))}
                >
                  <option value="disk">Disk</option>
                  <option value="s3">S3-compatible</option>
                </select>
                <EnvironmentNote locked={draft.backend.locked} />
              </Field>
              <Field data-disabled={draft.dir.locked || undefined}>
                <FieldLabel htmlFor="replay-directory">Replay directory</FieldLabel>
                <Input id="replay-directory" disabled={draft.dir.locked} value={draft.dir.value} onChange={(event) => setDraft((current) => ({ ...current, dir: { ...current.dir, value: event.target.value } }))} />
                <EnvironmentNote locked={draft.dir.locked} />
              </Field>
              {draft.backend.value === 's3' ? (
                <FieldGroup>
                  {(['endpoint', 'bucket', 'region', 'access_key', 'prefix'] as const).map((key) => (
                    <Field data-disabled={draft.s3.locked || undefined} key={key}>
                      <FieldLabel htmlFor={`replay-s3-${key}`}>{key.replace('_', ' ')}</FieldLabel>
                      <Input
                        id={`replay-s3-${key}`}
                        disabled={draft.s3.locked}
                        value={draft.s3.value[key]}
                        onChange={(event) => setDraft((current) => ({ ...current, s3: { ...current.s3, value: { ...current.s3.value, [key]: event.target.value } } }))}
                      />
                    </Field>
                  ))}
                  <FieldDescription>{draft.s3.value.secret_key_set ? 'A secret key is configured.' : 'No secret key is configured.'}</FieldDescription>
                  <EnvironmentNote locked={draft.s3.locked} />
                </FieldGroup>
              ) : null}
              <Button disabled={saving} type="submit">{saving ? 'Saving…' : 'Save storage settings'}</Button>
            </FieldGroup>
          </CardContent>
        </Card>
      </form>
      <AlertDialog open={confirming} onOpenChange={setConfirming}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Migrate existing replays?</AlertDialogTitle>
            <AlertDialogDescription>New recordings use the new backend immediately. Existing recordings can move in the background or remain where they are.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction disabled={saving} variant="outline" onClick={() => save(false)}>Leave in place</AlertDialogAction>
            <AlertDialogAction disabled={saving} onClick={() => save(true)}>Migrate in background</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
