import { type FormEvent, useState } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { LoaderCircleIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { activeLocale } from '@/lib/i18n/intl'
import { resetAfterLogin, requestLogout } from '@/lib/session-lifecycle'
import { applyPreferredTheme } from '@/lib/theme'
import { en } from '@/messages/en'
import { uiFlows } from '@/messages/uiFlows'

import { AuthScreen } from '@/features/auth/AuthScreen'
import { PasswordInput } from '@/features/auth/PasswordInput'
import { authProblemMessage, postPrivate, type LoginResult } from '@/features/auth/api'

export const Route = createFileRoute('/invite/$token')({
  component: InvitationRoute,
})

export type InvitationPreview = {
  org_name: string
  org_slug: string
  email: string
  role: 'owner' | 'admin' | 'editor' | 'viewer'
  invited_by: string
  expires_at: string
  account_exists: boolean
}

type SignedInUser = { email: string } | null

async function getJSON<T>(path: string): Promise<{ status: number; body: T | null }> {
  const response = await fetch(path, { credentials: 'same-origin', headers: { Accept: 'application/json' } })
  if (!response.ok) return { status: response.status, body: null }
  return { status: response.status, body: (await response.json()) as T }
}

async function loadPreview(token: string): Promise<InvitationPreview | null> {
  const { status, body } = await getJSON<InvitationPreview>(`/api/private/invitations/${encodeURIComponent(token)}`)
  if (status === 404 || status === 400) return null
  if (!body || typeof body.org_name !== 'string' || typeof body.email !== 'string') throw new Error('invitation unavailable')
  return body
}

// Checked without the API client: being signed out is normal on this page.
async function loadSignedInUser(): Promise<SignedInUser> {
  try {
    const { body } = await getJSON<{ user?: { email?: string } }>('/api/private/me')
    return typeof body?.user?.email === 'string' ? { email: body.user.email } : null
  } catch {
    return null
  }
}

function InvitationRoute() {
  const { token } = Route.useParams()
  const navigate = Route.useNavigate()
  const preview = useQuery({ queryKey: ['invitation-preview', token], queryFn: () => loadPreview(token), retry: false, staleTime: Infinity })
  const user = useQuery({ queryKey: ['invitation-user'], queryFn: loadSignedInUser, retry: false, staleTime: 0 })
  const acceptPath = `/api/private/invitations/${encodeURIComponent(token)}/accept`
  return (
    <InvitationPage
      token={token}
      preview={preview.data}
      previewFailed={preview.isError}
      loading={preview.isPending || user.isPending}
      user={user.data ?? null}
      accept={(body) => postPrivate(acceptPath, body)}
      signIn={(email, password) => postPrivate('/api/private/auth/login', { email, password })}
      onJoined={(slug) => {
        resetAfterLogin()
        applyPreferredTheme()
        window.location.assign(slug ? `/app/${encodeURIComponent(slug)}` : '/app')
      }}
      onNeedsSignIn={() => void navigate({ to: '/login', search: { notice: 'invite' }, replace: true })}
      onSignOut={() => void requestLogout(`/invite/${encodeURIComponent(token)}`)}
    />
  )
}

function formatExpiry(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString(activeLocale(), { dateStyle: 'medium', timeStyle: 'short' })
}

export function InvitationPage({ token, preview, previewFailed, loading, user, accept, signIn, onJoined, onNeedsSignIn, onSignOut }: {
  token: string
  preview: InvitationPreview | null | undefined
  previewFailed: boolean
  loading: boolean
  user: SignedInUser
  accept: (body: Record<string, string>) => Promise<LoginResult>
  signIn: (email: string, password: string) => Promise<LoginResult>
  onJoined: (orgSlug?: string) => void
  onNeedsSignIn: () => void
  onSignOut: () => void
}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')

  if (loading) {
    return <AuthScreen title={en.auth.invitationHeading} description={uiFlows.auth.loadingInvitation}><p role="status" className="text-sm text-muted-foreground">{uiFlows.auth.loadingInvitation}</p></AuthScreen>
  }
  if (previewFailed && !preview) {
    return (
      <AuthScreen title={en.auth.invitationHeading} description={en.auth.invitationDescription}>
        <p role="alert" className="text-sm text-destructive">{en.auth.serverError}</p>
        <Button type="button" variant="outline" onClick={() => window.location.reload()}>{uiFlows.common.retry}</Button>
      </AuthScreen>
    )
  }
  if (!preview) {
    return (
      <AuthScreen title={en.auth.invitationHeading} description={en.auth.invitationDescription}>
        <p role="alert" className="text-sm">{uiFlows.auth.inviteInvalid}</p>
        <Link to="/login" className="text-sm font-medium underline underline-offset-4">{uiFlows.auth.backToSignIn}</Link>
      </AuthScreen>
    )
  }

  const invited = preview.email.toLowerCase()
  const description = preview.invited_by ? uiFlows.auth.invitedBy(preview.invited_by, preview.org_name) : uiFlows.auth.invitedTo(preview.org_name)
  const details = (
    <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 rounded-lg border bg-muted/30 p-3 text-sm">
      <dt className="text-muted-foreground">{en.auth.email}</dt><dd className="break-all">{preview.email}</dd>
      <dt className="text-muted-foreground">{uiFlows.auth.role}</dt><dd>{uiFlows.auth.roles[preview.role] ?? preview.role}</dd>
      <dd className="col-span-2 text-xs text-muted-foreground">{uiFlows.auth.expires(formatExpiry(preview.expires_at))}</dd>
    </dl>
  )

  async function run(operation: () => Promise<void>) {
    if (pending) return
    setPending(true)
    setError('')
    try {
      await operation()
    } catch {
      setError(en.auth.networkError)
    } finally {
      setPending(false)
    }
  }

  const failed = (result: LoginResult) =>
    setError(result.type?.endsWith('password-policy') ? en.auth.passwordPolicy : authProblemMessage(result.problem))

  if (user) {
    if (user.email.toLowerCase() !== invited) {
      return (
        <AuthScreen title={uiFlows.auth.joinHeading(preview.org_name)} description={description}>
          {details}
          <p role="alert" className="text-sm">{uiFlows.auth.wrongAccount(user.email, preview.email)}</p>
          <Button type="button" variant="outline" onClick={onSignOut}>{uiFlows.auth.signOut}</Button>
        </AuthScreen>
      )
    }
    return (
      <AuthScreen title={uiFlows.auth.joinHeading(preview.org_name)} description={description}>
        {details}
        {error ? <FieldError>{error}</FieldError> : null}
        <Button
          type="button"
          disabled={pending}
          onClick={() => void run(async () => {
            const result = await accept({})
            if (result.ok) onJoined(preview.org_slug)
            else failed(result)
          })}
        >
          {pending ? <LoaderCircleIcon data-icon="inline-start" className="animate-spin" /> : null}
          {pending ? uiFlows.auth.joining : uiFlows.auth.joinAs(user.email)}
        </Button>
      </AuthScreen>
    )
  }

  if (preview.account_exists) {
    return (
      <AuthScreen title={uiFlows.auth.joinHeading(preview.org_name)} description={description}>
        {details}
        <p className="text-sm">{uiFlows.auth.accountExists(preview.email)}</p>
        <Button asChild>
          <Link to="/login" search={{ next: `/invite/${encodeURIComponent(token)}` }}>{uiFlows.auth.signInToAccept}</Link>
        </Button>
      </AuthScreen>
    )
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const name = String(form.get('name') ?? '')
    const password = String(form.get('password') ?? '')
    await run(async () => {
      const result = await accept({ name, password })
      if (!result.ok) {
        failed(result)
        return
      }
      // The new account exists now; sign in with the password just chosen.
      try {
        const login = await signIn(preview!.email, password)
        if (login.ok) {
          onJoined(preview!.org_slug)
          return
        }
      } catch {
        // Fall through to the sign-in page.
      }
      onNeedsSignIn()
    })
  }

  return (
    <AuthScreen title={uiFlows.auth.joinHeading(preview.org_name)} description={description}>
      {details}
      <form onSubmit={(event) => void submit(event)}>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="name">{en.auth.name}</FieldLabel>
            <Input id="name" name="name" autoComplete="name" required />
          </Field>
          <Field data-invalid={Boolean(error) || undefined}>
            <FieldLabel htmlFor="password">{en.auth.password}</FieldLabel>
            <PasswordInput
              id="password"
              name="password"
              autoComplete="new-password"
              minLength={6}
              required
              aria-invalid={Boolean(error) || undefined}
              aria-describedby={error ? 'invite-hint invite-error' : 'invite-hint'}
            />
            <FieldDescription id="invite-hint">{en.auth.passwordPolicy}</FieldDescription>
          </Field>
          {error ? <FieldError id="invite-error">{error}</FieldError> : null}
          <Button type="submit" disabled={pending}>
            {pending ? <LoaderCircleIcon data-icon="inline-start" className="animate-spin" /> : null}
            {pending ? uiFlows.auth.accepting : en.auth.acceptInvitation}
          </Button>
        </FieldGroup>
      </form>
    </AuthScreen>
  )
}
