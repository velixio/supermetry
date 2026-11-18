import { useState, type FormEvent } from 'react'

import { ErrorState } from '@/components/ErrorState'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription,
  AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { copyText } from '@/lib/clipboard'
import { formatCurrency, formatDateTime, formatNumber } from '@/lib/i18n/intl'
import { en } from '@/messages/en'

import {
  useConfigureStripe, useDisconnectStripe, useEnableStripe, useStripe, type StripeIntegration,
} from './api'

const t = en.integrationsUi
const s = t.stripe

export type StripeState = 'off' | 'pending' | 'active' | 'error'

export function stripeState(status?: StripeIntegration): StripeState {
  if (!status?.enabled) return 'off'
  if (!status.secret_configured) return 'pending'
  return status.last_error ? 'error' : 'active'
}

const stateLabel: Record<StripeState, string> = {
  off: s.statusOff,
  pending: s.statusPending,
  active: s.statusActive,
  error: s.statusError,
}

const stateClass: Record<StripeState, string> = {
  off: 'border-border text-muted-foreground',
  pending: 'border-amber-600 text-amber-700 dark:border-amber-400 dark:text-amber-300',
  active: 'border-emerald-600 text-emerald-700 dark:border-emerald-400 dark:text-emerald-300',
  error: 'border-destructive text-destructive',
}

const SECRET_PATTERN = /^whsec_[A-Za-z0-9+/=_-]{16,240}$/

// absoluteWebhookURL prefixes the dashboard origin when the server has no
// public base URL configured and returned only the path.
export function absoluteWebhookURL(url: string, origin = typeof window === 'undefined' ? '' : window.location.origin): string {
  return url.startsWith('/') ? `${origin}${url}` : url
}

function money(micros: number, currency?: string): string {
  const value = micros / 1_000_000
  if (!currency) return formatNumber(value)
  try {
    return formatCurrency(value, currency)
  } catch {
    return `${formatNumber(value)} ${currency}`
  }
}

function WebhookURL({ url }: { url: string }) {
  const [copied, setCopied] = useState<'idle' | 'ok' | 'failed'>('idle')
  const full = absoluteWebhookURL(url)
  return (
    <div className="flex flex-col gap-2">
      <label className="text-sm font-medium" htmlFor="stripe-webhook-url">{s.webhookUrl}</label>
      <div className="flex flex-wrap gap-2">
        <Input className="max-sm:min-h-11 min-w-0 flex-1 font-mono text-xs" id="stripe-webhook-url" onFocus={(event) => event.currentTarget.select()} readOnly value={full} />
        <Button
          className="max-sm:min-h-11"
          onClick={() => void copyText(full).then((ok) => setCopied(ok ? 'ok' : 'failed'))}
          type="button"
          variant="outline"
        >
          {s.copy}
        </Button>
      </div>
      <p aria-live="polite" className="text-sm text-muted-foreground">
        {copied === 'ok' ? s.copied : copied === 'failed' ? s.copyFailed : ''}
      </p>
    </div>
  )
}

function SecretForm({ status, siteId, orgId }: { status: StripeIntegration; siteId: number; orgId: number }) {
  const [secret, setSecret] = useState('')
  const [invalid, setInvalid] = useState(false)
  const configure = useConfigureStripe(siteId, orgId)

  function submit(event: FormEvent) {
    event.preventDefault()
    const value = secret.trim()
    if (!SECRET_PATTERN.test(value)) {
      setInvalid(true)
      return
    }
    setInvalid(false)
    configure.mutate({ signing_secret: value }, { onSuccess: () => setSecret('') })
  }

  return (
    <form className="flex flex-col gap-2" noValidate onSubmit={submit}>
      <label className="text-sm font-medium" htmlFor="stripe-signing-secret">{s.secretLabel}</label>
      <p className="text-sm text-muted-foreground" id="stripe-signing-secret-hint">
        {status.secret_configured ? s.secretReplaceHint : s.secretHint}
      </p>
      <div className="flex flex-wrap gap-2">
        <Input
          aria-describedby="stripe-signing-secret-hint"
          aria-invalid={invalid || undefined}
          autoComplete="off"
          className="max-sm:min-h-11 min-w-0 flex-1 font-mono"
          id="stripe-signing-secret"
          onChange={(event) => setSecret(event.target.value)}
          placeholder="whsec_…"
          spellCheck={false}
          type="password"
          value={secret}
        />
        <Button className="max-sm:min-h-11" disabled={configure.isPending} type="submit">
          {configure.isPending ? s.saving : s.saveSecret}
        </Button>
      </div>
      {invalid ? <p className="text-sm text-destructive" role="alert">{s.secretRequired}</p> : null}
      {configure.isError ? <p className="text-sm text-destructive" role="alert">{configure.error.message}</p> : null}
      {configure.isSuccess && !invalid ? <p className="text-sm text-muted-foreground" role="status">{s.saved}</p> : null}
    </form>
  )
}

function PaymentIntentToggle({ status, siteId, orgId }: { status: StripeIntegration; siteId: number; orgId: number }) {
  const configure = useConfigureStripe(siteId, orgId)
  return (
    <div className="flex flex-col gap-1">
      <label className="flex max-sm:min-h-11 items-center gap-3 text-sm font-medium">
        <input
          aria-describedby="stripe-payment-intents-hint"
          checked={status.record_payment_intents}
          className="size-4"
          disabled={configure.isPending}
          onChange={(event) => configure.mutate({ record_payment_intents: event.target.checked })}
          type="checkbox"
        />
        {s.paymentIntents}
      </label>
      <p className="text-sm text-muted-foreground" id="stripe-payment-intents-hint">{s.paymentIntentsHint}</p>
      {configure.isError ? <p className="text-sm text-destructive" role="alert">{configure.error.message}</p> : null}
    </div>
  )
}

function EventList({ events }: { events: string[] }) {
  return (
    <ul className="flex flex-wrap gap-2">
      {events.map((event) => (
        <li className="rounded-md border px-2 py-1 font-mono text-xs" key={event}>{event}</li>
      ))}
    </ul>
  )
}

function StatusDetails({ status }: { status: StripeIntegration }) {
  return (
    <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
      <div>
        <dt className="text-muted-foreground">{s.lastEvent}</dt>
        <dd className="break-words font-medium">
          {status.last_event_at
            ? `${formatDateTime(status.last_event_at, { dateStyle: 'medium', timeStyle: 'short' })}${status.last_event_type ? ` · ${status.last_event_type}` : ''}`
            : s.never}
        </dd>
      </div>
      <div>
        <dt className="text-muted-foreground">{s.eventsReceived}</dt>
        <dd className="font-medium">{formatNumber(status.events_received)}</dd>
      </div>
      <div>
        <dt className="text-muted-foreground">{s.revenueEvents}</dt>
        <dd className="font-medium">{formatNumber(status.revenue_events)}</dd>
      </div>
      <div>
        <dt className="text-muted-foreground">{s.refunds}</dt>
        <dd className="font-medium">
          {status.refunds_30d > 0 ? s.refundsValue(status.refunds_30d, money(status.refunded_micros_30d, status.revenue_currency)) : s.never}
        </dd>
      </div>
    </dl>
  )
}

export function StripeCard({ siteId, orgId, canManage }: { siteId: number; orgId: number; canManage: boolean }) {
  const stripe = useStripe(siteId, orgId)
  const enable = useEnableStripe(siteId, orgId)
  const disconnect = useDisconnectStripe(siteId, orgId)
  const [confirming, setConfirming] = useState(false)
  const status = stripe.data
  const state = stripeState(status)

  return (
    <section aria-labelledby="provider-stripe" className="flex min-w-0 flex-col gap-4 rounded-xl border bg-card p-5 text-card-foreground">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <h3 className="font-semibold" id="provider-stripe">{s.title}</h3>
        {status ? (
          <span className={`inline-flex min-h-7 items-center rounded-full border px-3 text-sm font-medium ${stateClass[state]}`}>
            <span className="sr-only">{t.statusLabel}: </span>
            {stateLabel[state]}
          </span>
        ) : null}
      </header>
      <p className="text-sm text-muted-foreground">{s.intro}</p>

      {stripe.isPending ? (
        <div aria-busy="true" role="status">
          <span className="sr-only">{t.loading}</span>
          <Skeleton className="h-24 w-full" />
        </div>
      ) : stripe.isError ? (
        <ErrorState detail={stripe.error.message} onRetry={() => void stripe.refetch()} />
      ) : !status?.enabled ? (
        canManage ? (
          <div>
            <Button className="max-sm:min-h-11" disabled={enable.isPending} onClick={() => enable.mutate()} type="button">
              {enable.isPending ? s.enabling : s.enable}
            </Button>
            {enable.isError ? <p className="mt-2 text-sm text-destructive" role="alert">{enable.error.message}</p> : null}
          </div>
        ) : null
      ) : (
        <>
          {status.revenue_currency
            ? <p className="text-sm">{s.currency(status.revenue_currency)}</p>
            : <p className="text-sm text-destructive" role="alert">{s.currencyMissing}</p>}
          {state === 'error' && status.last_error ? <p className="text-sm text-destructive" role="status">{s.lastError(status.last_error)}</p> : null}
          {status.last_event_livemode === false ? <p className="text-sm" role="status">{s.testMode}</p> : null}
          <StatusDetails status={status} />

          {canManage ? (
            <ol className="flex list-decimal flex-col gap-4 pl-5 text-sm">
              <li className="flex flex-col gap-2">
                <span>{s.step1}</span>
                {status.webhook_url ? <WebhookURL url={status.webhook_url} /> : null}
              </li>
              <li className="flex flex-col gap-2">
                <span>{s.step2}</span>
                <EventList events={status.events} />
                {status.optional_events.length > 0 ? (
                  <>
                    <span className="text-muted-foreground">{s.optionalEvents}</span>
                    <EventList events={status.optional_events} />
                  </>
                ) : null}
              </li>
              <li className="flex flex-col gap-2">
                <span>{s.step3}</span>
                <SecretForm orgId={orgId} siteId={siteId} status={status} />
              </li>
            </ol>
          ) : null}
          {canManage ? <PaymentIntentToggle orgId={orgId} siteId={siteId} status={status} /> : null}
          <p className="text-sm text-muted-foreground">{s.attributionHint}</p>

          {canManage ? (
            <div>
              <Button className="max-sm:min-h-11" onClick={() => setConfirming(true)} type="button" variant="outline">
                {s.disconnect}
              </Button>
            </div>
          ) : null}
        </>
      )}
      {!canManage ? <p className="text-sm text-muted-foreground">{s.readOnly}</p> : null}

      <AlertDialog open={confirming} onOpenChange={setConfirming}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{s.disconnectTitle}</AlertDialogTitle>
            <AlertDialogDescription>{s.disconnectBody}</AlertDialogDescription>
          </AlertDialogHeader>
          {disconnect.isError ? <p role="alert">{disconnect.error.message}</p> : null}
          <AlertDialogFooter>
            <AlertDialogCancel className="max-sm:min-h-11">{t.cancel}</AlertDialogCancel>
            <AlertDialogAction
              className="max-sm:min-h-11"
              disabled={disconnect.isPending}
              onClick={(event) => {
                event.preventDefault()
                disconnect.mutate(undefined, { onSuccess: () => setConfirming(false) })
              }}
              variant="destructive"
            >
              {disconnect.isPending ? t.disconnecting : s.disconnectConfirm}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}
