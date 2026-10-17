import { useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { nativeSelectClass } from '@/lib/nativeSelect'
import { retentionOptions, retentionPolicy, type RetentionPolicy } from '@/lib/instanceConfig'
import { uiFlows } from '@/messages/uiFlows'

import { TimezoneSelect } from './TimezoneSelect'
import type { RetentionDays, WebSiteInput } from './types'

export { TimezoneSelect } from './TimezoneSelect'

export function retentionLabel(days: number): string {
  return days === 0 ? uiFlows.site.forever : uiFlows.site.days(days)
}

type FieldName = 'domain' | 'timezone' | 'retention' | 'currency'

/** Which field a server problem is about, so the message appears next to it. */
export function siteErrorField(message: string): FieldName {
  const text = message.toLowerCase()
  if (text.includes('timezone') || text.includes('time zone')) return 'timezone'
  if (text.includes('currency')) return 'currency'
  if (text.includes('retention') || text.includes('forever')) return 'retention'
  return 'domain'
}

/** The site name a hostname suggests: example.com for www.example.com. */
export function nameFromDomain(host: string): string {
  return host.replace(/^www\./i, '')
}

/**
 * The first-site form: a domain and a time zone. Retention and the permanent revenue currency
 * sit under "Advanced options" with safe defaults, so nothing irreversible blocks the first run.
 */
export function SiteForm({
  submitLabel,
  onSubmit,
  secondaryAction,
  retention,
}: {
  submitLabel: string
  onSubmit: (input: WebSiteInput) => Promise<void>
  secondaryAction?: React.ReactNode
  /** The instance policy once loaded. Until then keep-forever stays hidden and the server's default applies. */
  retention?: RetentionPolicy
}) {
  const policy = retention ?? { ...retentionPolicy(undefined), forever: false }
  // Only a retention the user picked is sent; otherwise the server applies the instance default.
  const [retentionChosen, setRetentionChosen] = useState(false)
  const [error, setError] = useState<{ field: FieldName; message: string } | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [advancedOpen, setAdvancedOpen] = useState(false)

  function focusField(field: FieldName) {
    const ids: Record<FieldName, string> = { domain: 'site-domains', timezone: 'site-timezone', retention: 'site-retention', currency: 'site-revenue-currency' }
    if (field === 'retention' || field === 'currency') setAdvancedOpen(true)
    requestAnimationFrame(() => document.getElementById(ids[field])?.focus())
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (submitting) return
    setError(null)
    setSubmitting(true)
    const data = new FormData(event.currentTarget)
    const hosts = String(data.get('domains') ?? '')
      .split(',')
      .map((host) => host.trim().replace(/^https?:\/\//i, '').replace(/\/.*$/, ''))
      .filter(Boolean)
    const currency = String(data.get('revenue_currency') ?? '').trim().toUpperCase()
    try {
      await onSubmit({
        name: nameFromDomain(hosts[0] ?? ''),
        hosts,
        timezone: String(data.get('timezone') ?? 'UTC').trim(),
        ...(retentionChosen ? { retention_days: Number(data.get('retention')) as RetentionDays } : {}),
        ...(currency ? { revenue_currency: currency } : {}),
      })
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : uiFlows.site.createFailed
      const field = siteErrorField(message)
      setError({ field, message })
      focusField(field)
    } finally {
      setSubmitting(false)
    }
  }

  const errorFor = (field: FieldName) => (error?.field === field ? error.message : '')
  const domainError = errorFor('domain')
  const retentionError = errorFor('retention')
  const currencyError = errorFor('currency')

  return (
    <form className="flex flex-col gap-5" onSubmit={(event) => void submit(event)}>
      <FieldGroup>
        <Field data-invalid={Boolean(domainError) || undefined}>
          <FieldLabel htmlFor="site-domains">{uiFlows.site.domain}</FieldLabel>
          <Input
            id="site-domains"
            name="domains"
            inputMode="url"
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            placeholder="example.com…"
            aria-invalid={Boolean(domainError) || undefined}
            aria-describedby={domainError ? 'site-domains-hint site-domains-error' : 'site-domains-hint'}
            required
          />
          <FieldDescription id="site-domains-hint">{uiFlows.site.domainHint}</FieldDescription>
          {domainError ? <FieldError id="site-domains-error">{domainError}</FieldError> : null}
        </Field>
        <TimezoneSelect error={errorFor('timezone')} hint={uiFlows.site.timezoneHint} />
        <details
          className="group rounded-lg border px-3 py-2"
          open={advancedOpen}
          onToggle={(event) => setAdvancedOpen((event.currentTarget as HTMLDetailsElement).open)}
        >
          <summary className="cursor-pointer text-sm font-medium select-none">{uiFlows.site.advanced}</summary>
          <FieldGroup className="pt-4 pb-2">
            <Field data-invalid={Boolean(retentionError) || undefined}>
              <FieldLabel htmlFor="site-retention">{uiFlows.site.retention}</FieldLabel>
              <select
                id="site-retention"
                name="retention"
                key={`${policy.defaultDays}:${policy.forever}`}
                defaultValue={String(policy.defaultDays)}
                onChange={() => setRetentionChosen(true)}
                className={nativeSelectClass}
                aria-invalid={Boolean(retentionError) || undefined}
                aria-describedby={retentionError ? 'site-retention-hint site-retention-error' : 'site-retention-hint'}
              >
                {retentionOptions(policy).map((days) => (
                  <option key={days} value={days}>{retentionLabel(days)}</option>
                ))}
              </select>
              <FieldDescription id="site-retention-hint">{uiFlows.site.retentionHint}</FieldDescription>
              {retentionError ? <FieldError id="site-retention-error">{retentionError}</FieldError> : null}
            </Field>
            <Field data-invalid={Boolean(currencyError) || undefined}>
              <FieldLabel htmlFor="site-revenue-currency">{uiFlows.site.currency}</FieldLabel>
              <Input
                id="site-revenue-currency"
                name="revenue_currency"
                autoCapitalize="characters"
                autoComplete="off"
                spellCheck={false}
                maxLength={3}
                pattern="[A-Za-z]{3}"
                placeholder="USD…"
                className="max-w-32 uppercase"
                onInvalid={() => setAdvancedOpen(true)}
                aria-invalid={Boolean(currencyError) || undefined}
                aria-describedby={currencyError ? 'site-currency-hint site-currency-error' : 'site-currency-hint'}
              />
              <FieldDescription id="site-currency-hint">{uiFlows.site.currencyHint}</FieldDescription>
              {currencyError ? <FieldError id="site-currency-error">{currencyError}</FieldError> : null}
            </Field>
          </FieldGroup>
        </details>
      </FieldGroup>
      <div className="flex flex-wrap gap-2">
        <Button type="submit" disabled={submitting}>
          {submitting ? uiFlows.site.creating : submitLabel}
        </Button>
        {secondaryAction}
      </div>
    </form>
  )
}
