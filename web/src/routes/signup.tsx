import { type FormEvent, useEffect, useState } from 'react'
import { createFileRoute, Link, redirect } from '@tanstack/react-router'
import { LoaderCircleIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { en } from '@/messages/en'
import { uiFlows } from '@/messages/uiFlows'
import { resetAfterLogin } from '@/lib/session-lifecycle'
import { signedInRedirect } from '@/lib/signed-in'
import { applyPreferredTheme } from '@/lib/theme'

import { AuthScreen } from '@/features/auth/AuthScreen'
import { PasswordInput } from '@/features/auth/PasswordInput'
import { authProblemMessage, createAuthConfigApi, postPrivate, type LoginResult, type SignupMode } from '@/features/auth/api'

export type SignupInput = {
  email: string
  name: string
  password: string
  org_name: string
}

export const Route = createFileRoute('/signup')({
  beforeLoad: async () => {
    const target = await signedInRedirect('/app')
    if (target) throw redirect({ href: target, replace: true })
  },
  component: SignupPage,
})

function SignupPage() {
  const navigate = Route.useNavigate()
  const [signup, setSignup] = useState<SignupMode>()
  const [configFailed, setConfigFailed] = useState(false)
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    let active = true
    setConfigFailed(false)
    void createAuthConfigApi()()
      .then((config) => {
        if (active) setSignup(config.signup)
      })
      .catch(() => {
        if (active) setConfigFailed(true)
      })
    return () => {
      active = false
    }
  }, [attempt])

  return (
    <SignupPageContent
      signup={signup}
      configFailed={configFailed}
      onRetryConfig={() => setAttempt((value) => value + 1)}
      submit={(input) => postPrivate('/api/private/auth/signup', input)}
      onSuccess={() => {
        resetAfterLogin()
        applyPreferredTheme()
        void navigate({ to: '/', replace: true })
      }}
    />
  )
}

export function SignupPageContent({
  signup,
  submit,
  onSuccess,
  configFailed = false,
  onRetryConfig,
}: {
  signup?: SignupMode
  submit: (input: SignupInput) => Promise<LoginResult>
  onSuccess: (path: '/') => void
  /** The sign-up settings could not be loaded; never guess "invite-only" from a failure. */
  configFailed?: boolean
  onRetryConfig?: () => void
}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')

  if (configFailed && signup === undefined) {
    return (
      <AuthScreen title={en.auth.signupHeading} description={en.auth.signupDescription}>
        <p role="alert" className="text-sm text-destructive">{uiFlows.auth.signupConfigFailed}</p>
        {onRetryConfig ? <Button type="button" variant="outline" onClick={onRetryConfig}>{uiFlows.common.retry}</Button> : null}
        <p className="text-sm text-muted-foreground">
          Already have an account? <Link to="/login">{en.auth.backToLogin}</Link>
        </p>
      </AuthScreen>
    )
  }

  if (signup === undefined) {
    return (
      <AuthScreen title={en.auth.signupHeading} description={en.auth.signupDescription}>
        <p role="status" className="text-sm text-muted-foreground">Loading signup settings…</p>
      </AuthScreen>
    )
  }

  if (signup === 'invite') {
    return (
      <AuthScreen title={en.auth.signupHeading} description={en.auth.inviteOnly}>
        <p className="text-sm text-muted-foreground">
          Already have an account? <Link to="/login">{en.auth.backToLogin}</Link>
        </p>
      </AuthScreen>
    )
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    setPending(true)
    setError('')
    try {
      const result = await submit({
        name: String(form.get('name') ?? ''),
        email: String(form.get('email') ?? ''),
        password: String(form.get('password') ?? ''),
        org_name: String(form.get('org_name') ?? ''),
      })
      if (result.ok) {
        onSuccess('/')
      } else {
        setError(result.type?.endsWith('password-policy') ? en.auth.passwordPolicy : authProblemMessage(result.problem))
      }
    } catch (reason) {
      setError(reason instanceof TypeError ? en.auth.networkError : en.auth.serverError)
    } finally {
      setPending(false)
    }
  }

  return (
    <AuthScreen title={en.auth.signupHeading} description={en.auth.signupDescription}>
      <form onSubmit={handleSubmit}>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="name">{en.auth.name}</FieldLabel>
            <Input id="name" name="name" autoComplete="name" required />
          </Field>
          <Field>
            <FieldLabel htmlFor="email">{en.auth.email}</FieldLabel>
            <Input id="email" name="email" type="email" autoComplete="email" spellCheck={false} autoCapitalize="none" required />
          </Field>
          <Field>
            <FieldLabel htmlFor="password">{en.auth.password}</FieldLabel>
            <PasswordInput id="password" name="password" autoComplete="new-password" minLength={6} required />
			<FieldDescription>{en.auth.passwordPolicy}</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="org_name">{en.auth.organizationName}</FieldLabel>
            <Input id="org_name" name="org_name" autoComplete="organization" required />
          </Field>
          {error ? <FieldError>{error}</FieldError> : null}
          <Button type="submit" disabled={pending}>
            {pending ? <LoaderCircleIcon data-icon="inline-start" /> : null}
            {pending ? en.auth.creatingAccount : en.auth.createAccount}
          </Button>
        </FieldGroup>
      </form>
      <p className="text-sm text-muted-foreground">
        Already have an account? <Link to="/login">{en.auth.backToLogin}</Link>
      </p>
    </AuthScreen>
  )
}
