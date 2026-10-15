import { useState, type FormEvent } from 'react'
import { CircleAlertIcon, ExternalLinkIcon, InfoIcon, LoaderCircleIcon, RefreshCwIcon } from 'lucide-react'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { useMe } from '@/lib/me'
import { en } from '@/messages/en'
import { AdminPage, formatDate } from './AdminCommon'
import { useApplyUpdate, useCheckUpdate, useUpdateStatus, type AdminUpdateJob } from './adminApi'

const terminalStates = new Set<AdminUpdateJob['status']>(['complete', 'failed', 'recovery_required'])

function newIdempotencyKey() {
  const bytes = globalThis.crypto.getRandomValues(new Uint8Array(16))
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
}

function VersionValue({ value }: { value: string | null | undefined }) {
  return <dd className="break-all text-sm font-medium">{value || en.admin.unknownVersion}</dd>
}

function deploymentLabel(mode: AdminUpdateJob['deployment_mode'] | undefined) {
  if (mode === 'binary') return en.admin.binaryInstallation
  if (mode === 'docker') return en.admin.dockerInstallation
  return en.admin.unknownInstallation
}

function UpdateJobStatus({ job }: { job: AdminUpdateJob }) {
  const active = !terminalStates.has(job.status)
  if (job.status === 'recovery_required') {
    return (
      <Alert variant="destructive">
        <CircleAlertIcon aria-hidden="true" />
        <AlertTitle>{en.admin.recoveryRequired}</AlertTitle>
        <AlertDescription>{en.admin.recoveryRequiredDescription}</AlertDescription>
      </Alert>
    )
  }
  if (job.status === 'failed') {
    return (
      <Alert variant="destructive">
        <CircleAlertIcon aria-hidden="true" />
        <AlertTitle>{en.admin.updateFailed}</AlertTitle>
        <AlertDescription>{en.admin.updateFailedDescription}</AlertDescription>
      </Alert>
    )
  }
  return (
    <Card role="status" aria-label={en.admin.updateProgress} aria-live={active ? 'polite' : 'off'}>
      <CardHeader>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <CardTitle>{active ? en.admin.updateInProgress : en.admin.updateComplete}</CardTitle>
          <Badge variant={active ? 'secondary' : 'outline'}>
            {active ? <LoaderCircleIcon aria-hidden="true" /> : null}
            {en.admin.updateStages[job.status]}
          </Badge>
        </div>
        <CardDescription>{active ? en.admin.updateInProgressDescription : en.admin.updateCompleteDescription}</CardDescription>
      </CardHeader>
      <CardContent>
        <dl className="grid gap-x-6 gap-y-4 sm:grid-cols-2">
          <div className="flex min-w-0 flex-col gap-1"><dt className="text-sm text-muted-foreground">{en.admin.targetVersion}</dt><VersionValue value={job.target_version} /></div>
          <div className="flex min-w-0 flex-col gap-1"><dt className="text-sm text-muted-foreground">{en.admin.installationType}</dt><VersionValue value={deploymentLabel(job.deployment_mode)} /></div>
          <div className="flex min-w-0 flex-col gap-1"><dt className="text-sm text-muted-foreground">{en.admin.updateStarted}</dt><VersionValue value={formatDate(job.created_at)} /></div>
          <div className="flex min-w-0 flex-col gap-1"><dt className="text-sm text-muted-foreground">{en.admin.lastChange}</dt><VersionValue value={formatDate(job.updated_at)} /></div>
        </dl>
      </CardContent>
    </Card>
  )
}

function ApplyUpdateDialog({ targetVersion, totpEnabled }: { targetVersion: string; totpEnabled: boolean }) {
  const apply = useApplyUpdate()
  const [open, setOpen] = useState(false)
  const [password, setPassword] = useState('')
  const [totpCode, setTotpCode] = useState('')
  const [idempotencyKey, setIdempotencyKey] = useState('')
  const confirmed = password.length > 0 && (!totpEnabled || /^\d{6}$/.test(totpCode))

  function changeOpen(next: boolean) {
    setOpen(next)
    if (next) {
      setIdempotencyKey(newIdempotencyKey())
      return
    }
    setPassword('')
    setTotpCode('')
    setIdempotencyKey('')
    apply.reset()
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!confirmed || !idempotencyKey) return
    apply.mutate({
      target_version: targetVersion,
      idempotency_key: idempotencyKey,
      password,
      ...(totpEnabled ? { totp_code: totpCode } : {}),
    }, { onSuccess: () => changeOpen(false) })
  }

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogTrigger asChild>
        <Button><RefreshCwIcon data-icon="inline-start" aria-hidden="true" />{en.admin.updateTo.replace('{version}', targetVersion)}</Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{en.admin.confirmUpdateTitle.replace('{version}', targetVersion)}</DialogTitle>
          <DialogDescription>{en.admin.confirmUpdateDescription}</DialogDescription>
        </DialogHeader>
        <Alert>
          <InfoIcon aria-hidden="true" />
          <AlertTitle>{en.admin.backupBeforeUpdate}</AlertTitle>
          <AlertDescription>{en.admin.backupBeforeUpdateDescription}</AlertDescription>
        </Alert>
        <form className="flex flex-col gap-4" onSubmit={submit}>
          <FieldGroup>
            <Field data-invalid={apply.isError ? true : undefined}>
              <FieldLabel htmlFor="update-password">{en.admin.currentPassword}</FieldLabel>
              <Input id="update-password" type="password" autoComplete="current-password" value={password} aria-invalid={apply.isError ? true : undefined} onChange={(event) => setPassword(event.currentTarget.value)} required />
            </Field>
            {totpEnabled ? (
              <Field data-invalid={totpCode.length > 0 && !/^\d{6}$/.test(totpCode) ? true : undefined}>
                <FieldLabel htmlFor="update-totp">{en.admin.authenticationCode}</FieldLabel>
                <Input id="update-totp" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" maxLength={6} value={totpCode} aria-invalid={totpCode.length > 0 && !/^\d{6}$/.test(totpCode) ? true : undefined} onChange={(event) => setTotpCode(event.currentTarget.value)} required />
              </Field>
            ) : null}
            {apply.error ? <FieldError>{apply.error.message}</FieldError> : null}
          </FieldGroup>
          <DialogFooter>
            <DialogClose asChild><Button type="button" variant="outline">{en.admin.cancel}</Button></DialogClose>
            <Button type="submit" disabled={!confirmed || apply.isPending}>
              {apply.isPending ? <LoaderCircleIcon data-icon="inline-start" aria-hidden="true" /> : null}
              {apply.isPending ? en.admin.startingUpdate : en.admin.startUpdate}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function AdminUpdatePage() {
  const status = useUpdateStatus(true)
  const check = useCheckUpdate()
  const me = useMe()
  const errors = [status.error instanceof Error ? status.error.message : status.error, check.error instanceof Error ? check.error.message : undefined, status.data?.check_error]
    .filter((error): error is string => typeof error === 'string' && error.length > 0)
  const job = status.data?.job
  const activeJob = Boolean(job && !terminalStates.has(job.status))
  const canApply = Boolean(status.data?.update_supported && status.data.update_available && status.data.latest_version && !activeJob && job?.status !== 'recovery_required')

  return (
    <AdminPage title={en.admin.update} description={en.admin.updateDescription}>
      {errors.length > 0 ? <Alert variant="destructive"><AlertTitle>{en.admin.updateCheckFailed}</AlertTitle><AlertDescription>{errors.join(' ')}</AlertDescription></Alert> : null}
      {status.isPending ? <div role="status" aria-label={en.admin.loading} className="flex max-w-xl flex-col gap-3"><Skeleton className="h-32 w-full" /><Skeleton className="h-20 w-full" /></div> : null}
      {job ? <div className="max-w-xl"><UpdateJobStatus job={job} /></div> : null}
      {status.data ? (
        <Card className="max-w-xl">
          <CardHeader>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <CardTitle>{en.admin.versionStatus}</CardTitle>
              {status.data.deployment_mode ? <Badge variant="outline">{deploymentLabel(status.data.deployment_mode)}</Badge> : null}
            </div>
            <CardDescription>{status.data.check_error ? en.admin.updateUnknown : status.data.update_available ? en.admin.newRelease : en.admin.upToDate}</CardDescription>
          </CardHeader>
          <CardContent>
            <dl className="grid gap-x-6 gap-y-4 sm:grid-cols-2">
              <div className="flex min-w-0 flex-col gap-1"><dt className="text-sm text-muted-foreground">{en.admin.currentVersion}</dt><VersionValue value={status.data.current_version} /></div>
              <div className="flex min-w-0 flex-col gap-1"><dt className="text-sm text-muted-foreground">{en.admin.latestVersion}</dt><VersionValue value={status.data.latest_version} /></div>
              <div className="flex min-w-0 flex-col gap-1"><dt className="text-sm text-muted-foreground">{en.admin.published}</dt><VersionValue value={status.data.published_at ? formatDate(status.data.published_at) : null} /></div>
              <div className="flex min-w-0 flex-col gap-1"><dt className="text-sm text-muted-foreground">{en.admin.checked}</dt><VersionValue value={status.data.checked_at ? formatDate(status.data.checked_at) : null} /></div>
            </dl>
          </CardContent>
          <CardFooter className="flex flex-wrap gap-2">
            <Button type="button" variant="outline" disabled={check.isPending} onClick={() => check.mutate()}>
              {check.isPending ? <LoaderCircleIcon data-icon="inline-start" aria-hidden="true" className="animate-spin" /> : <RefreshCwIcon data-icon="inline-start" aria-hidden="true" />}
              {check.isPending ? en.admin.checkingForUpdates : en.admin.checkForUpdates}
            </Button>
            {canApply && status.data.latest_version ? <ApplyUpdateDialog targetVersion={status.data.latest_version} totpEnabled={Boolean(me.data?.user.totp_enabled)} /> : null}
            {status.data.release_url ? <Button asChild variant="outline"><a href={status.data.release_url} target="_blank" rel="noreferrer">{en.admin.viewRelease}<ExternalLinkIcon data-icon="inline-end" aria-hidden="true" /></a></Button> : null}
          </CardFooter>
        </Card>
      ) : null}
      {status.data && !status.data.update_supported ? (
        <Alert role="status" className="max-w-xl"><InfoIcon aria-hidden="true" /><AlertTitle>{en.admin.installUnavailable}</AlertTitle><AlertDescription>{en.admin.installUnavailableDescription}</AlertDescription></Alert>
      ) : null}
    </AdminPage>
  )
}
