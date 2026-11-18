import { useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ApiProblem } from '@/lib/apiProblem'
import { isTotpRequired, type ReauthSubmit } from '@/lib/reauth'
import { uiFoundation } from '@/messages/uiFoundation'

type ReauthDialogProps = {
  open: boolean
  /** Resolves when the server accepted the credentials; rejects (ApiProblem) otherwise. */
  onSubmit: ReauthSubmit
  /** Called when the user dismisses the dialog without confirming. */
  onCancel: () => void
  /** Show the authentication-code field from the start (account has two-factor on). */
  requireTotp?: boolean
}

export function ReauthDialog({ open, onSubmit, onCancel, requireTotp = false }: ReauthDialogProps) {
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [showCode, setShowCode] = useState(requireTotp)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const t = uiFoundation.reauth

  async function submit(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      await onSubmit({ password, ...(showCode && code ? { totp_code: code } : {}) })
      setPassword('')
      setCode('')
    } catch (err) {
      if (isTotpRequired(err)) setShowCode(true)
      setError(err instanceof ApiProblem && (err.status === 401 || err.status === 403 || err.status === 400) ? t.invalid : t.failed)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next && !busy) onCancel() }}>
      <DialogContent className="sm:max-w-sm">
        <form onSubmit={submit} className="grid gap-4" noValidate>
          <DialogHeader>
            <DialogTitle>{t.title}</DialogTitle>
            <DialogDescription>{t.description}</DialogDescription>
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor="reauth-password">{t.password}</Label>
            <Input
              id="reauth-password"
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              aria-invalid={error ? true : undefined}
              aria-describedby={error ? 'reauth-error' : undefined}
            />
          </div>
          {showCode ? (
            <div className="grid gap-2">
              <Label htmlFor="reauth-code">{t.totpCode}</Label>
              <Input
                id="reauth-code"
                inputMode="numeric"
                autoComplete="one-time-code"
                value={code}
                onChange={(e) => setCode(e.target.value)}
                aria-describedby="reauth-code-hint"
              />
              <p id="reauth-code-hint" className="text-xs text-muted-foreground">{t.totpHint}</p>
            </div>
          ) : null}
          {error ? <p id="reauth-error" role="alert" className="text-sm text-destructive">{error}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onCancel} disabled={busy}>{t.cancel}</Button>
            <Button type="submit" disabled={busy || password === ''}>{busy ? t.submitting : t.submit}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
