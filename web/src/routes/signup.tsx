import { type FormEvent, useEffect, useState } from 'react'
import { createFileRoute, Link, redirect } from '@tanstack/react-router'
import { LoaderCircleIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { en } from '@/messages/en'
import { resetAfterLogin } from '@/lib/session-lifecycle'
import { signedInRedirect } from '@/lib/signed-in'
import { applyPreferredTheme } from '@/lib/theme'

import {
  AuthScreen,
  createAuthConfigApi,
  postPrivate,
  type LoginResult,
  type SignupMode,
} from './login'

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

  useEffect(() => {
    let active = true
    void createAuthConfigApi()()
      .then((config) => {
        if (active) setSignup(config.signup)
      })
      .catch(() => {
        if (active) setSignup('invite')
      })
    return () => {
      active = false
    }
  }, [])

  return (
    <SignupPageContent
      signup={signup}
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
}: {
  signup?: SignupMode
  submit: (input: SignupInput) => Promise<LoginResult>
  onSuccess: (path: '/') => void
}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')

  if (signup === undefined) {
    return (
      <AuthScreen title={en.auth.signupHeading} description={en.auth.signupDescription}>
        <p className="text-sm text-muted-foreground">Loading signup settings…</p>
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
		setError(result.type === 'password-policy' ? en.auth.passwordPolicy : en.auth.signupError)
      }
    } catch {
      setError(en.auth.signupError)
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
            <Input id="email" name="email" type="email" autoComplete="email" required />
          </Field>
          <Field>
            <FieldLabel htmlFor="password">{en.auth.password}</FieldLabel>
			<Input id="password" name="password" type="password" autoComplete="new-password" minLength={6} required />
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
