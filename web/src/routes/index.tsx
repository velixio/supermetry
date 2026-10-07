import { useEffect, useState, type FormEvent } from 'react'
import { createFileRoute } from '@tanstack/react-router'

import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  createFirstSite,
  type CreatedSite,
  type CreateSiteInput,
} from '@/features/share/api'
import { en } from '@/messages/en'
import { useMe, type Me } from './$org'

export const Route = createFileRoute('/')({
  component: IndexPage,
})

function IndexPage() {
  const me = useMe()

  if (me.isError) {
    return <OnboardingError detail="Your account could not be loaded." />
  }
  if (!me.data) {
    return (
      <main className="grid min-h-dvh place-items-center px-6">
        <p className="text-sm text-muted-foreground">{en.loadingDashboard}</p>
      </main>
    )
  }

  return <Onboarding me={me.data} />
}

function siteSlug(site: Me['memberships'][number]['sites'][number]): string {
  return site.slug ?? site.public_id ?? String(site.id)
}

function OnboardingError({ detail }: { detail: string }) {
  return (
    <main className="grid min-h-dvh place-items-center px-6">
      <div className="flex max-w-sm flex-col gap-2 border-l-2 border-destructive pl-5">
        <h1 className="text-2xl font-semibold tracking-tight">{en.appName}</h1>
        <p role="alert" className="text-sm text-muted-foreground">{detail}</p>
      </div>
    </main>
  )
}

export function Onboarding({
  me,
  createSite = createFirstSite,
}: {
  me: Me
  createSite?: (input: CreateSiteInput) => Promise<CreatedSite>
}) {
  const membership = me.memberships[0]
  const existingSite = membership?.sites[0]
  const [createdSite, setCreatedSite] = useState<CreatedSite>()
  const [name, setName] = useState('')
  const [host, setHost] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (!membership || !existingSite) return
    window.location.replace(`/${membership.org.slug}/${siteSlug(existingSite)}`)
  }, [existingSite, membership])

  if (!membership) {
    return <OnboardingError detail="Your account does not belong to an organization." />
  }

  if (existingSite) {
    return (
      <main className="grid min-h-dvh place-items-center px-6">
        <p className="text-sm text-muted-foreground">{en.loadingDashboard}</p>
      </main>
    )
  }

  if (createdSite) {
    const snippet = `<script defer data-site="${createdSite.public_id}" src="/tracker.js"></script>`
    return (
      <main className="grid min-h-dvh place-items-center px-6 py-10">
        <section className="flex w-full max-w-2xl flex-col gap-6 border-l-2 border-accent pl-5">
          <div className="flex flex-col gap-1">
            <h1 className="text-2xl font-semibold tracking-tight">Add the tracking snippet</h1>
            <p className="text-sm text-muted-foreground">
              Place this before the closing head tag on {createdSite.allowed_hosts[0]}.
            </p>
          </div>
          <pre className="overflow-x-auto rounded-lg border bg-muted p-4 text-sm"><code>{snippet}</code></pre>
          <p role="status" className="text-sm text-muted-foreground">
            Waiting for the first event…
          </p>
        </section>
      </main>
    )
  }

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setError('')
    setSubmitting(true)
    try {
      const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
      const site = await createSite({
        name: name.trim(),
        hosts: [host.trim()],
        timezone,
        retention_days: 30,
      })
      setCreatedSite(site)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The site could not be created.')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="grid min-h-dvh place-items-center px-6 py-10">
      <form className="w-full max-w-md border-l-2 border-accent pl-5" onSubmit={submit}>
        <FieldGroup>
          <div className="flex flex-col gap-1">
            <h1 className="text-2xl font-semibold tracking-tight">Create your first site</h1>
            <p className="text-sm text-muted-foreground">
              Add the website where Panel should collect analytics.
            </p>
          </div>
          <Field>
            <FieldLabel htmlFor="site-name">Site name</FieldLabel>
            <Input
              id="site-name"
              autoComplete="organization"
              required
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
          </Field>
          <Field data-invalid={Boolean(error) || undefined}>
            <FieldLabel htmlFor="site-host">Website host</FieldLabel>
            <Input
              id="site-host"
              inputMode="url"
              placeholder="example.com"
              required
              value={host}
              aria-invalid={Boolean(error) || undefined}
              onChange={(event) => setHost(event.target.value)}
            />
            <FieldDescription>Enter the hostname without a path.</FieldDescription>
            <FieldError>{error}</FieldError>
          </Field>
          <Button type="submit" disabled={submitting}>
            {submitting ? 'Creating…' : 'Create site'}
          </Button>
        </FieldGroup>
      </form>
    </main>
  )
}
