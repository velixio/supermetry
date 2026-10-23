import { CheckIcon, CopyIcon } from 'lucide-react'
import { useState, type FormEvent } from 'react'

import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { en } from '@/messages/en'
import { ErrorLine, formatDate } from '@/features/admin/AdminCommon'
import {
  useAddStarsRepo, useDeleteStarsRepo, useRotateStarsSecret, useSyncStarsRepo, useUpdateStarsRepo,
  type StarsRepo, type StarsRepoSecret,
} from './githubStarsApi'

const t = en.githubStars

/** What a create or a rotation returns: shown once, never retrievable again. */
export type Reveal = Pick<StarsRepoSecret, 'webhook_secret' | 'webhook_url'> & { repo: string }

function CopyButton({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false)
  const copy = () => {
    void navigator.clipboard?.writeText(text).then(() => {
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    })
  }
  return (
    <Button type="button" variant="outline" className="min-h-11 shrink-0" aria-label={label} onClick={copy}>
      {copied ? <CheckIcon aria-hidden="true" /> : <CopyIcon aria-hidden="true" />}
      <span aria-live="polite">{copied ? t.copied : t.copy}</span>
    </Button>
  )
}

function SecretField({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <span className="text-sm font-medium">{label}</span>
      <div className="flex items-center gap-2">
        <code className="min-w-0 flex-1 break-all rounded-md border bg-muted px-2 py-2 text-xs">{value}</code>
        <CopyButton text={value} label={t.copyLabel(label)} />
      </div>
    </div>
  )
}

/** The one-time panel with the webhook URL and secret and the GitHub setup steps. */
export function RevealPanel({ reveal, onDismiss }: { reveal: Reveal; onDismiss: () => void }) {
  return (
    <section aria-labelledby="stars-reveal" className="flex min-w-0 flex-col gap-3 rounded-lg border border-primary/40 bg-muted/40 p-4">
      <h2 id="stars-reveal" className="text-base font-semibold">{t.revealTitle(reveal.repo)}</h2>
      <p role="alert" className="text-sm">{t.revealWarn}</p>
      <SecretField label={t.webhookUrl} value={reveal.webhook_url} />
      <SecretField label={t.webhookSecret} value={reveal.webhook_secret} />
      <div className="flex flex-col gap-1">
        <h3 className="text-sm font-semibold">{t.setupTitle}</h3>
        <ol className="list-decimal pl-5 text-sm text-muted-foreground">
          {[t.setup1, t.setup2, t.setup3, t.setup4, t.setup5].map((step) => <li key={step}>{step}</li>)}
        </ol>
      </div>
      <Button type="button" className="min-h-11 self-start" onClick={onDismiss}>{t.revealDismiss}</Button>
    </section>
  )
}

export function AddRepoDialog({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (open: boolean) => void; onCreated: (reveal: Reveal, id: number) => void }) {
  const add = useAddStarsRepo()
  const [repo, setRepo] = useState('')
  const [token, setToken] = useState('')
  const [zone, setZone] = useState('UTC')
  const close = (next: boolean) => {
    if (!next) { add.reset(); setRepo(''); setToken(''); setZone('UTC') }
    onOpenChange(next)
  }
  const submit = (event: FormEvent) => {
    event.preventDefault()
    add.mutate(
      { repo: repo.trim(), time_zone: zone.trim() || 'UTC', ...(token.trim() ? { token: token.trim() } : {}) },
      { onSuccess: (result) => { onCreated({ repo: result.repo.repo, webhook_secret: result.webhook_secret, webhook_url: result.webhook_url }, result.repo.id); close(false) } },
    )
  }
  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        <form className="flex flex-col gap-3" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{t.addRepo}</DialogTitle>
            <DialogDescription>{t.addRepoDescription}</DialogDescription>
          </DialogHeader>
          <label className="flex flex-col gap-1 text-sm font-medium">
            {t.repoName}
            <Input className="min-h-11" value={repo} required maxLength={140} placeholder="owner/name" autoComplete="off" onChange={(event) => setRepo(event.target.value)} />
          </label>
          <label className="flex flex-col gap-1 text-sm font-medium">
            {t.repoToken}
            <Input className="min-h-11" type="password" value={token} autoComplete="off" onChange={(event) => setToken(event.target.value)} />
            <span className="text-xs font-normal text-muted-foreground">{t.repoTokenHint}</span>
          </label>
          <label className="flex flex-col gap-1 text-sm font-medium">
            {t.repoTimeZone}
            <Input className="min-h-11" value={zone} required maxLength={64} onChange={(event) => setZone(event.target.value)} />
            <span className="text-xs font-normal text-muted-foreground">{t.repoTimeZoneHint}</span>
          </label>
          <ErrorLine error={add.error} />
          <DialogFooter>
            <Button type="button" variant="outline" className="min-h-11" onClick={() => close(false)}>{t.cancel}</Button>
            <Button type="submit" className="min-h-11" disabled={add.isPending || !repo.trim()}>{add.isPending ? t.adding : t.addRepo}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function SettingsDialog({ repo, open, onOpenChange }: { repo: StarsRepo; open: boolean; onOpenChange: (open: boolean) => void }) {
  const update = useUpdateStarsRepo()
  const [zone, setZone] = useState(repo.time_zone)
  const [token, setToken] = useState('')
  const [clear, setClear] = useState(false)
  const submit = (event: FormEvent) => {
    event.preventDefault()
    update.mutate(
      { id: repo.id, patch: { time_zone: zone.trim(), ...(clear ? { clear_token: true } : token.trim() ? { token: token.trim() } : {}) } },
      { onSuccess: () => { setToken(''); setClear(false); onOpenChange(false) } },
    )
  }
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) update.reset(); onOpenChange(next) }}>
      <DialogContent>
        <form className="flex flex-col gap-3" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{t.settingsTitle(repo.repo)}</DialogTitle>
            <DialogDescription>{t.settingsDescription}</DialogDescription>
          </DialogHeader>
          <label className="flex flex-col gap-1 text-sm font-medium">
            {t.repoTimeZone}
            <Input className="min-h-11" value={zone} required maxLength={64} onChange={(event) => setZone(event.target.value)} />
          </label>
          <label className="flex flex-col gap-1 text-sm font-medium">
            {t.repoToken}
            <Input className="min-h-11" type="password" value={token} disabled={clear} autoComplete="off" onChange={(event) => setToken(event.target.value)} />
            <span className="text-xs font-normal text-muted-foreground">{repo.has_token ? t.tokenStored : t.repoTokenHint}</span>
          </label>
          {repo.has_token ? (
            <label className="flex min-h-11 items-center gap-2 text-sm">
              <input type="checkbox" className="size-5" checked={clear} onChange={(event) => setClear(event.target.checked)} />
              {t.clearToken}
            </label>
          ) : null}
          <ErrorLine error={update.error} />
          <DialogFooter>
            <Button type="button" variant="outline" className="min-h-11" onClick={() => onOpenChange(false)}>{t.cancel}</Button>
            <Button type="submit" className="min-h-11" disabled={update.isPending}>{update.isPending ? t.saving : t.save}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function RemoveDialog({ repo, open, onOpenChange, onRemoved }: { repo: StarsRepo; open: boolean; onOpenChange: (open: boolean) => void; onRemoved?: () => void }) {
  const remove = useDeleteStarsRepo()
  const [confirmData, setConfirmData] = useState(false)
  const close = (next: boolean) => {
    if (!next) { remove.reset(); setConfirmData(false) }
    onOpenChange(next)
  }
  const run = (deleteData: boolean) => remove.mutate({ id: repo.id, deleteData }, { onSuccess: () => { close(false); if (deleteData) onRemoved?.() } })
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!remove.isPending) close(next) }}>
      <DialogContent>
        {confirmData ? (
          <>
            <DialogHeader>
              <DialogTitle>{t.removeDataTitle(repo.repo)}</DialogTitle>
              <DialogDescription>{t.removeDataBody}</DialogDescription>
            </DialogHeader>
            <ErrorLine error={remove.error} />
            <DialogFooter>
              <Button type="button" variant="outline" className="min-h-11" disabled={remove.isPending} onClick={() => setConfirmData(false)}>{t.back}</Button>
              <Button type="button" variant="destructive" className="min-h-11" disabled={remove.isPending} onClick={() => run(true)}>{remove.isPending ? t.removing : t.removeDataConfirm}</Button>
            </DialogFooter>
          </>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>{t.removeTitle(repo.repo)}</DialogTitle>
              <DialogDescription>{t.removeBody}</DialogDescription>
            </DialogHeader>
            <ErrorLine error={remove.error} />
            <div className="flex flex-col gap-2">
              <Button type="button" className="min-h-11 justify-start" disabled={remove.isPending || !repo.enabled} onClick={() => run(false)}>{t.disableKeep}</Button>
              <p className="text-xs text-muted-foreground">{t.disableKeepHint}</p>
              <Button type="button" variant="destructive" className="min-h-11 justify-start" disabled={remove.isPending} onClick={() => setConfirmData(true)}>{t.removeData}</Button>
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" className="min-h-11" onClick={() => close(false)}>{t.cancel}</Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

function RotateDialog({ repo, open, onOpenChange, onRotated }: { repo: StarsRepo; open: boolean; onOpenChange: (open: boolean) => void; onRotated: (reveal: Reveal) => void }) {
  const rotate = useRotateStarsSecret()
  return (
    <AlertDialog open={open} onOpenChange={(next) => { if (!rotate.isPending) { if (!next) rotate.reset(); onOpenChange(next) } }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t.rotateTitle(repo.repo)}</AlertDialogTitle>
          <AlertDialogDescription>{t.rotateBody}</AlertDialogDescription>
        </AlertDialogHeader>
        <ErrorLine error={rotate.error} />
        <AlertDialogFooter>
          <AlertDialogCancel disabled={rotate.isPending}>{t.cancel}</AlertDialogCancel>
          <AlertDialogAction disabled={rotate.isPending} onClick={(event) => {
            event.preventDefault()
            rotate.mutate(repo.id, { onSuccess: (result) => { onRotated({ repo: result.repo.repo, webhook_secret: result.webhook_secret, webhook_url: result.webhook_url }); onOpenChange(false) } })
          }}>{rotate.isPending ? t.rotating : t.rotateConfirm}</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/** Status line and actions of the selected repository. */
/** `onRemoved` runs after the repository and its data were removed (not after a plain disable). */
export function RepoPanel({ repo, onReveal, onRemoved }: { repo: StarsRepo; onReveal: (reveal: Reveal) => void; onRemoved?: () => void }) {
  const update = useUpdateStarsRepo()
  const sync = useSyncStarsRepo()
  const [dialog, setDialog] = useState<'settings' | 'rotate' | 'remove' | null>(null)
  const switchId = `stars-enabled-${repo.id}`
  return (
    <section aria-label={t.statusTitle(repo.repo)} className="flex min-w-0 flex-col gap-3 rounded-lg border p-3">
      <dl className="grid gap-x-6 gap-y-1 text-sm sm:grid-cols-2">
        <div><dt className="inline text-muted-foreground">{t.lastWebhook}: </dt><dd className="inline">{repo.last_webhook_at ? formatDate(repo.last_webhook_at) : t.neverWebhook}</dd></div>
        <div><dt className="inline text-muted-foreground">{t.lastSync}: </dt><dd className="inline">{repo.last_reconcile_at ? formatDate(repo.last_reconcile_at) : t.neverSynced}</dd></div>
        <div><dt className="inline text-muted-foreground">{t.repoTimeZone}: </dt><dd className="inline">{repo.time_zone}</dd></div>
        <div><dt className="inline text-muted-foreground">{t.webhookUrl}: </dt><dd className="inline break-all">{repo.webhook_url}</dd></div>
      </dl>
      <div className="flex min-w-0 flex-col gap-1">
        <span className="text-sm text-muted-foreground">{t.publicCountUrl}</span>
        <div className="flex items-center gap-2">
          <code className="min-w-0 flex-1 break-all rounded-md border bg-muted px-2 py-2 text-xs">{repo.public_count_url}</code>
          <CopyButton text={repo.public_count_url} label={t.copyLabel(t.publicCountUrl)} />
        </div>
        <span className="text-xs text-muted-foreground">{t.publicCountHint}</span>
      </div>
      {repo.last_error ? <p role="alert" className="text-sm text-destructive">{t.lastError}: {repo.last_error}</p> : null}
      <div className="flex flex-wrap items-center gap-2">
        <label htmlFor={switchId} className="flex min-h-11 items-center gap-2 pr-2 text-sm">
          <Switch id={switchId} checked={repo.enabled} disabled={update.isPending}
            onCheckedChange={(enabled) => update.mutate({ id: repo.id, patch: { enabled } })} />
          {t.enabled}
        </label>
        <Button type="button" variant="outline" className="min-h-11" disabled={sync.isPending || !repo.enabled} onClick={() => sync.mutate(repo.id)}>{sync.isPending ? t.syncing : t.syncNow}</Button>
        <Button type="button" variant="outline" className="min-h-11" onClick={() => setDialog('settings')}>{t.settings}</Button>
        <Button type="button" variant="outline" className="min-h-11" onClick={() => setDialog('rotate')}>{t.rotate}</Button>
        <Button type="button" variant="outline" className="min-h-11" onClick={() => setDialog('remove')}>{t.remove}</Button>
      </div>
      {sync.isSuccess ? <p role="status" className="text-sm">{t.syncStarted}</p> : null}
      <ErrorLine error={update.error ?? sync.error} />
      <SettingsDialog key={`s-${repo.id}-${repo.time_zone}-${repo.has_token}`} repo={repo} open={dialog === 'settings'} onOpenChange={(open) => setDialog(open ? 'settings' : null)} />
      <RotateDialog repo={repo} open={dialog === 'rotate'} onOpenChange={(open) => setDialog(open ? 'rotate' : null)} onRotated={onReveal} />
      <RemoveDialog repo={repo} open={dialog === 'remove'} onOpenChange={(open) => setDialog(open ? 'remove' : null)} onRemoved={onRemoved} />
    </section>
  )
}
