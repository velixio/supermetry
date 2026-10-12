import { type FormEvent, useState } from 'react'
import { createFileRoute, redirect } from '@tanstack/react-router'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { en } from '@/messages/en'
import { signedInRedirect } from '@/lib/signed-in'

import { AuthScreen } from '@/features/auth/AuthScreen'
import { authResultMessage, postPrivate } from '@/features/auth/api'

const parseSetupSearch = (value: unknown): { token?: string } => {
  const search = value && typeof value === 'object' ? value as Record<string, unknown> : {}
  return typeof search.token === 'string' ? { token: search.token } : {}
}

export const Route = createFileRoute('/setup')({
  validateSearch: parseSetupSearch,
  beforeLoad: async ({ search }) => {
    const destination = await setupRouteDestination(search.token)
    if (destination === '/app' || destination === '/login') throw redirect({ href: destination, replace: true })
    return { invalid: destination === 'invalid' }
  },
  component: SetupPage,
})

export async function setupRouteDestination(
  token: string | undefined,
  fetch: typeof globalThis.fetch = (...args) => globalThis.fetch(...args),
): Promise<'/app' | '/login' | 'invalid' | null> {
  const signedIn = await signedInRedirect('/app', fetch)
  if (signedIn) return '/app'
  if (!token) return 'invalid'
  try {
    const response = await fetch('/api/private/setup/status', {
      credentials: 'same-origin',
      headers: { Accept: 'application/json' },
    })
    if (!response.ok) return 'invalid'
    const status = (await response.json()) as { available?: unknown }
    return status.available === true ? null : 'invalid'
  } catch {
    return 'invalid'
  }
}

function SetupPage() {
  const { token = '' } = Route.useSearch()
  const { invalid } = Route.useRouteContext()

  if (invalid || !token) return <AuthScreen title={en.auth.setupHeading} description={en.auth.setupDescription}><p role="alert">{en.auth.invalidToken}</p></AuthScreen>

  return <SetupForm token={token} />
}

export function SetupForm({ token }: { token: string }) {
  const [message, setMessage] = useState('')

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const result = await authResultMessage(() =>
      postPrivate('/api/private/setup', {
        token,
        email: String(form.get('email') ?? ''),
        name: String(form.get('name') ?? ''),
        password: String(form.get('password') ?? ''),
        org_name: String(form.get('org_name') ?? ''),
      }),
    )
    setMessage(result)
    if (result === en.auth.success) window.location.assign('/login')
  }

  return (
    <AuthScreen title={en.auth.setupHeading} description={en.auth.setupDescription}>
      <form onSubmit={submit}>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="name">{en.auth.name}</FieldLabel>
            <Input id="name" name="name" autoComplete="name" required />
          </Field>
          <Field>
            <FieldLabel htmlFor="email">{en.auth.email}</FieldLabel>
            <Input id="email" name="email" type="email" autoComplete="email" required />
          </Field>
          <Field>
            <FieldLabel htmlFor="org_name">{en.auth.organizationName}</FieldLabel>
            <Input id="org_name" name="org_name" required />
          </Field>
          <Field>
            <FieldLabel htmlFor="password">{en.auth.password}</FieldLabel>
			<Input id="password" name="password" type="password" minLength={6} required />
			<FieldDescription>{en.auth.passwordPolicy}</FieldDescription>
          </Field>
          {message ? message === en.auth.success ? <p role="status" className="text-sm text-muted-foreground">{message}</p> : <FieldError>{message}</FieldError> : null}
          <Button type="submit">{en.auth.completeSetup}</Button>
        </FieldGroup>
      </form>
    </AuthScreen>
  )
}
