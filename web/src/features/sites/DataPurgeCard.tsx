import { useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

import { createPurge, listPurges, type PurgeRequest } from './purgeApi'

const active = (request: PurgeRequest) => ['pending', 'running', 'settling'].includes(request.status)

function describe(request: PurgeRequest): string {
  const filters = [request.source && `source ${request.source}`, request.country && `country ${request.country}`].filter(Boolean).join(' and ')
  const range = request.from === request.to ? request.from : `${request.from} to ${request.to}`
  const result = active(request)
    ? 'in progress'
    : request.status === 'done'
      ? `${request.sessions_deleted} sessions deleted`
      : `failed${request.error ? `: ${request.error}` : ''}`
  return `${range}, ${filters}: ${result}`
}

export function DataPurgeCard({ orgId, siteId }: { orgId: number; siteId: number }) {
  const queryClient = useQueryClient()
  const [error, setError] = useState('')
  const queryKey = ['site', siteId, 'purges']
  const purges = useQuery({
    queryKey,
    queryFn: () => listPurges(siteId, orgId),
    refetchInterval: (query) => (query.state.data?.data.some(active) ? 5000 : false),
  })
  const mutation = useMutation({
    mutationFn: (form: FormData) =>
      createPurge(siteId, orgId, {
        from: String(form.get('from') ?? ''),
        to: String(form.get('to') ?? ''),
        source: String(form.get('source') ?? '').trim(),
        country: String(form.get('country') ?? '').trim(),
        password: String(form.get('password') ?? ''),
        totp_code: String(form.get('totp_code') ?? '').trim() || undefined,
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey }),
  })
  const busy = purges.data?.data.some(active) ?? false

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = event.currentTarget
    setError('')
    try {
      await mutation.mutateAsync(new FormData(form))
      form.reset()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The request could not be started.')
    }
  }

  return (
    <section className="rounded-xl border bg-card p-5" aria-labelledby="data-purge-title">
      <h2 id="data-purge-title" className="text-lg font-semibold">Delete stored traffic</h2>
      <p className="mt-1 text-sm text-muted-foreground">
        Permanently removes sessions and their events that match a source and/or a country, within a date range (in this site&apos;s time zone). Reports for those days are rebuilt afterwards. Session recordings are not deleted. This cannot be undone.
      </p>
      <form className="mt-4 flex flex-col gap-4" onSubmit={(event) => void submit(event)}>
        <FieldGroup>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="purge-from">From</FieldLabel>
              <Input id="purge-from" name="from" type="date" required />
            </Field>
            <Field>
              <FieldLabel htmlFor="purge-to">To</FieldLabel>
              <Input id="purge-to" name="to" type="date" required />
            </Field>
          </div>
          <Field>
            <FieldLabel htmlFor="purge-source">Source</FieldLabel>
            <Input id="purge-source" name="source" placeholder="spam-site.example" autoComplete="off" />
            <FieldDescription>The source name exactly as the Sources report shows it. Leave empty to match on country only.</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="purge-country">Country</FieldLabel>
            <Input id="purge-country" name="country" placeholder="US" maxLength={2} autoComplete="off" className="w-24 uppercase" />
            <FieldDescription>A two-letter country code. Fill in a source, a country, or both.</FieldDescription>
          </Field>
          <Field data-invalid={Boolean(error) || undefined}>
            <FieldLabel htmlFor="purge-password">Your password</FieldLabel>
            <Input id="purge-password" name="password" type="password" autoComplete="current-password" required aria-invalid={Boolean(error) || undefined} />
            <FieldError>{error}</FieldError>
          </Field>
          <Field>
            <FieldLabel htmlFor="purge-totp">Authentication code</FieldLabel>
            <Input id="purge-totp" name="totp_code" inputMode="numeric" autoComplete="one-time-code" className="w-40" />
            <FieldDescription>Only needed if two-factor authentication is on for your account.</FieldDescription>
          </Field>
        </FieldGroup>
        <Button type="submit" variant="destructive" className="self-start" disabled={mutation.isPending || busy}>
          {busy ? 'A deletion is in progress' : mutation.isPending ? 'Starting…' : 'Delete matching traffic'}
        </Button>
      </form>
      {purges.data && purges.data.data.length > 0 ? (
        <div className="mt-5">
          <h3 className="text-sm font-medium">Recent deletions</h3>
          <ul className="mt-2 flex flex-col gap-1 text-sm text-muted-foreground">
            {purges.data.data.map((request) => (
              <li key={request.id}>{describe(request)}</li>
            ))}
          </ul>
        </div>
      ) : null}
    </section>
  )
}
