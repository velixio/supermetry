import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { useState, type FormEvent } from 'react'

import { ErrorState } from '@/components/ErrorState'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription,
  AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { fmt } from '@/lib/format'
import { en } from '@/messages/en'

import {
  SEARCH_CONSOLE, useConnectIntegration, useDisconnectIntegration, useIntegrations,
  type IntegrationConnection, type IntegrationEntry,
} from './api'
import { SearchConsoleReport } from './SearchConsoleReport'

const t = en.integrationsUi

export type IntegrationResult = 'connected' | 'denied' | 'error'
export type ConnectionState = 'none' | 'active' | 'reauth' | 'error'

export function connectionState(connection?: IntegrationConnection): ConnectionState {
  if (!connection) return 'none'
  if (connection.status === 'reauth') return 'reauth'
  return connection.last_error ? 'error' : 'active'
}

const stateLabel: Record<ConnectionState, string> = {
  none: t.statusNone,
  active: t.statusActive,
  reauth: t.statusReauth,
  error: t.statusError,
}

const stateClass: Record<ConnectionState, string> = {
  none: 'border-border text-muted-foreground',
  active: 'border-emerald-600 text-emerald-700 dark:border-emerald-400 dark:text-emerald-300',
  reauth: 'border-amber-600 text-amber-700 dark:border-amber-400 dark:text-amber-300',
  error: 'border-destructive text-destructive',
}

const BACKFILL_OPTIONS = [28, 90, 180, 485] as const

function resultMessage(result: IntegrationResult): string {
  return result === 'connected' ? t.resultConnected : result === 'denied' ? t.resultDenied : t.resultError
}

function ConnectForm({
  entry, canManage, siteId, orgId,
}: { entry: IntegrationEntry; canManage: boolean; siteId: number; orgId: number }) {
  const [property, setProperty] = useState(entry.connection?.property ?? '')
  const [backfillDays, setBackfillDays] = useState<number>(28)
  const [invalid, setInvalid] = useState(false)
  const connect = useConnectIntegration(siteId, orgId)
  const reconnecting = Boolean(entry.connection)

  if (!canManage) return null

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const trimmed = property.trim()
    if (!trimmed) {
      setInvalid(true)
      return
    }
    setInvalid(false)
    connect.mutate(
      { provider: entry.provider, property: trimmed, backfillDays },
      { onSuccess: (url) => window.location.assign(url) },
    )
  }

  const pending = connect.isPending || connect.isSuccess
  return (
    <form aria-labelledby={`connect-${entry.provider}`} className="flex flex-col gap-3 border-t pt-4" onSubmit={submit} noValidate>
      <h3 className="text-sm font-medium" id={`connect-${entry.provider}`}>{t.connectHeading}</h3>
      <div className="flex flex-col gap-1">
        <label className="text-sm font-medium" htmlFor={`property-${entry.provider}`}>{t.propertyLabel}</label>
        <Input
          aria-describedby={`property-hint-${entry.provider}`}
          aria-invalid={invalid || undefined}
          autoComplete="off"
          className="min-h-11"
          id={`property-${entry.provider}`}
          onChange={(event) => setProperty(event.target.value)}
          placeholder="sc-domain:supermetry.com"
          spellCheck={false}
          value={property}
        />
        <p className="text-xs text-muted-foreground" id={`property-hint-${entry.provider}`}>{t.propertyHint}</p>
        {invalid ? <p className="text-sm text-destructive" role="alert">{t.propertyRequired}</p> : null}
      </div>
      <div className="flex flex-col gap-1 sm:max-w-xs">
        <label className="text-sm font-medium" htmlFor={`backfill-${entry.provider}`}>{t.backfillLabel}</label>
        <select
          className="min-h-11 rounded-lg border border-input bg-background px-2.5 text-sm"
          id={`backfill-${entry.provider}`}
          onChange={(event) => setBackfillDays(Number(event.target.value))}
          value={backfillDays}
        >
          {BACKFILL_OPTIONS.map((days) => <option key={days} value={days}>{t.backfillOption(days)}</option>)}
        </select>
      </div>
      {connect.isError ? <p className="text-sm text-destructive" role="alert">{connect.error.message}</p> : null}
      <div>
        <Button className="min-h-11" disabled={pending} type="submit">
          {pending ? t.connecting : reconnecting ? t.reconnect : t.connect}
        </Button>
      </div>
    </form>
  )
}

function ProviderCard({
  entry, canManage, siteId, orgId,
}: { entry: IntegrationEntry; canManage: boolean; siteId: number; orgId: number }) {
  const [confirming, setConfirming] = useState(false)
  const disconnect = useDisconnectIntegration(siteId, orgId)
  const state = connectionState(entry.connection)
  const connection = entry.connection
  const headingId = `provider-${entry.provider}`

  return (
    <section aria-labelledby={headingId} className="flex min-w-0 flex-col gap-4 rounded-xl border bg-card p-5 text-card-foreground">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <h3 className="font-semibold" id={headingId}>{entry.name}</h3>
        <span className={`inline-flex min-h-7 items-center rounded-full border px-3 text-sm font-medium ${stateClass[state]}`}>
          <span className="sr-only">{t.statusLabel}: </span>
          {stateLabel[state]}
        </span>
      </header>

      {connection ? (
        <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-3">
          <div className="min-w-0">
            <dt className="text-muted-foreground">{t.property}</dt>
            <dd className="break-words font-medium">{connection.property}</dd>
          </div>
          <div>
            <dt className="text-muted-foreground">{t.lastSync}</dt>
            <dd className="font-medium">{connection.last_sync_at ? fmt.date(connection.last_sync_at, 'UTC') : t.never}</dd>
          </div>
          <div>
            <dt className="text-muted-foreground">{t.syncedThrough}</dt>
            <dd className="font-medium">{connection.synced_through ? fmt.date(connection.synced_through, 'UTC') : t.never}</dd>
          </div>
        </dl>
      ) : null}

      {state === 'reauth' ? <p className="text-sm" role="status">{t.reauthBody}</p> : null}
      {state === 'error' && connection ? <p className="text-sm" role="status">{t.errorBody(connection.last_error ?? '')}</p> : null}

      {connection && canManage ? (
        <div>
          <Button className="min-h-11" onClick={() => setConfirming(true)} type="button" variant="outline">
            {t.disconnect}
            <span className="sr-only"> {entry.name}</span>
          </Button>
        </div>
      ) : null}
      {disconnect.isError ? <p className="text-sm text-destructive" role="alert">{disconnect.error.message}</p> : null}

      {state === 'none' || state === 'reauth' ? <ConnectForm entry={entry} canManage={canManage} orgId={orgId} siteId={siteId} /> : null}
      {!canManage ? <p className="text-sm text-muted-foreground">{t.readOnly}</p> : null}

      <AlertDialog open={confirming} onOpenChange={setConfirming}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t.disconnectTitle}</AlertDialogTitle>
            <AlertDialogDescription>{t.disconnectBody}</AlertDialogDescription>
          </AlertDialogHeader>
          {disconnect.isError ? <p role="alert">{disconnect.error.message}</p> : null}
          <AlertDialogFooter>
            <AlertDialogCancel className="min-h-11">{t.cancel}</AlertDialogCancel>
            <AlertDialogAction
              className="min-h-11"
              disabled={disconnect.isPending}
              onClick={(event) => {
                event.preventDefault()
                disconnect.mutate(entry.provider, { onSuccess: () => setConfirming(false) })
              }}
              variant="destructive"
            >
              {disconnect.isPending ? t.disconnecting : t.disconnectConfirm}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}

export function IntegrationsPage({
  siteId, orgId, role, result, onDismissResult,
}: {
  siteId: number
  orgId: number
  role: string
  result?: IntegrationResult
  onDismissResult?: () => void
}) {
  const integrations = useIntegrations(siteId, orgId)
  const canManage = role === 'owner' || role === 'admin'
  const searchConsole = integrations.data?.find((entry) => entry.provider === SEARCH_CONSOLE)

  return (
    <PageContainer width="narrow">
      <PageHeader title={t.title} description={t.intro} />

      {result ? (
        <div
          className={`flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3 text-sm ${result === 'connected' ? 'border-emerald-600' : 'border-destructive'}`}
          role={result === 'connected' ? 'status' : 'alert'}
        >
          <p>{resultMessage(result)}</p>
          {onDismissResult ? <Button className="min-h-11" onClick={onDismissResult} type="button" variant="ghost">{t.dismiss}</Button> : null}
        </div>
      ) : null}

      {integrations.isPending ? (
        <div aria-busy="true" role="status">
          <span className="sr-only">{t.loading}</span>
          <Skeleton className="h-40 w-full" />
        </div>
      ) : integrations.isError ? (
        <ErrorState level="page" detail={t.loadError} onRetry={() => void integrations.refetch()} />
      ) : integrations.data.length === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>{t.notConfiguredTitle}</EmptyTitle>
            <EmptyDescription>{t.notConfiguredBody}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <section aria-labelledby="integration-providers" className="flex flex-col gap-4">
          <h2 className="text-sm font-medium text-muted-foreground" id="integration-providers">{t.providerHeading}</h2>
          {integrations.data.map((entry) => (
            <ProviderCard canManage={canManage} entry={entry} key={entry.provider} orgId={orgId} siteId={siteId} />
          ))}
        </section>
      )}

      {searchConsole?.connection ? <SearchConsoleReport orgId={orgId} siteId={siteId} /> : null}
    </PageContainer>
  )
}
