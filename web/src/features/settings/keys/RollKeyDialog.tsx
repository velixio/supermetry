import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field, FieldError, FieldLabel } from '@/components/ui/field'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Input } from '@/components/ui/input'
import { useCopyStatus } from '@/features/settings/InstallRecipes'
import { privateRequest } from '@/features/settings/privateApi'
import { activeLocale } from '@/lib/i18n/intl'
import { useReauth } from '@/lib/useReauth'
import { uiSettings } from '@/messages/uiSettings'

import { relativeTime } from './keyStatus'

type Grace = '0' | '1' | '24' | '168'
type Rolled = { id: number; key: string; expires_at: string | null; old_key_expires_at?: string | null }

export function RollKeyDialog({ orgId, apiKey, onClose }: { orgId: number; apiKey: { id: number; name: string } | null; onClose: () => void }) {
  return (
    <Dialog open={apiKey !== null} onOpenChange={(open) => { if (!open) onClose() }}>
      {apiKey ? <RollBody orgId={orgId} apiKey={apiKey} onClose={onClose} /> : null}
    </Dialog>
  )
}

function RollBody({ orgId, apiKey, onClose }: { orgId: number; apiKey: { id: number; name: string }; onClose: () => void }) {
  const t = uiSettings.roll
  const queryClient = useQueryClient()
  const { withReauth, reauthDialog } = useReauth()
  const [grace, setGrace] = useState<Grace>('24')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [rolled, setRolled] = useState<Rolled | null>(null)
  const { copied, copy } = useCopyStatus()

  async function submit() {
    setBusy(true)
    setError('')
    try {
      const result = await withReauth(() =>
        privateRequest<Rolled>('POST', `/api/private/orgs/${orgId}/api-keys/${apiKey.id}/roll`, { orgId, body: { grace_hours: Number(grace) } }),
      )
      setRolled(result)
      void queryClient.invalidateQueries({ queryKey: ['api-keys', orgId] })
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <DialogContent
        onInteractOutside={(event) => { if (rolled) event.preventDefault() }}
        onEscapeKeyDown={(event) => { if (rolled) event.preventDefault() }}
        showCloseButton={!rolled}
      >
        <DialogHeader>
          <DialogTitle>{rolled ? t.doneTitle : t.title(apiKey.name)}</DialogTitle>
          <DialogDescription>{rolled ? uiSettings.wizard.wontSeeAgain : t.description}</DialogDescription>
        </DialogHeader>
        {rolled ? (
          <div className="flex flex-col gap-3">
            <Field>
              <FieldLabel htmlFor="rolled-api-key">{uiSettings.wizard.secretLabel}</FieldLabel>
              <div className="flex gap-2">
                <Input id="rolled-api-key" readOnly value={rolled.key} className="font-mono text-xs" onFocus={(event) => event.currentTarget.select()} />
                <Button type="button" onClick={() => void copy(rolled.key)}>{uiSettings.wizard.copyKey}</Button>
              </div>
              <span role="status" aria-live="polite" className="text-sm text-muted-foreground">{copied === 'yes' ? uiSettings.wizard.copied : ''}</span>
            </Field>
            <p className="text-sm text-muted-foreground">
              {rolled.old_key_expires_at ? t.oldKeyEnds(relativeTime(rolled.old_key_expires_at, activeLocale())) : t.oldKeyNow}
            </p>
            <DialogFooter><Button type="button" onClick={onClose}>{uiSettings.wizard.finish}</Button></DialogFooter>
          </div>
        ) : (
          <form className="flex flex-col gap-4" onSubmit={(event) => { event.preventDefault(); void submit() }}>
            <Field>
              <FieldLabel htmlFor="roll-grace">{t.graceLabel}</FieldLabel>
              <Select value={grace} onValueChange={(value) => setGrace(value as Grace)}>
                <SelectTrigger id="roll-grace" className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {(Object.keys(t.options) as Grace[]).map((value) => <SelectItem key={value} value={value}>{t.options[value]}</SelectItem>)}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </Field>
            {error ? <FieldError>{error}</FieldError> : null}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={onClose}>{uiSettings.keys.cancel}</Button>
              <Button type="submit" disabled={busy} aria-busy={busy}>{t.submit}</Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
      {reauthDialog}
    </>
  )
}
