import { type FormEvent, useState } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import { LoaderCircleIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { en } from '@/messages/en'
import { uiFlows } from '@/messages/uiFlows'

import { AuthScreen } from '@/features/auth/AuthScreen'
import { PasswordInput } from '@/features/auth/PasswordInput'
import { authProblemMessage, postPrivate, type LoginResult } from '@/features/auth/api'

export const Route = createFileRoute('/reset/$token')({
  component: ResetRoute,
})

function ResetRoute() {
  const { token } = Route.useParams()
  const navigate = Route.useNavigate()
  return (
    <ResetPasswordForm
      submit={(password) => postPrivate('/api/private/auth/password-reset/confirm', { token, password })}
      onDone={() => void navigate({ to: '/login', search: { notice: 'reset' }, replace: true })}
    />
  )
}

export function ResetPasswordForm({ submit, onDone }: { submit: (password: string) => Promise<LoginResult>; onDone: () => void }) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const [invalidLink, setInvalidLink] = useState(false)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (pending) return
    const password = String(new FormData(event.currentTarget).get('password') ?? '')
    setPending(true)
    setError('')
    try {
      const result = await submit(password)
      if (result.ok) {
        onDone()
        return
      }
      const message = result.type?.endsWith('password-policy') ? en.auth.passwordPolicy : authProblemMessage(result.problem)
      setInvalidLink(message === en.auth.invalidToken)
      setError(message)
    } catch {
      setError(en.auth.networkError)
    } finally {
      setPending(false)
    }
  }

  return (
    <AuthScreen title={en.auth.resetHeading} description={en.auth.resetDescription}>
      <form onSubmit={(event) => void handleSubmit(event)}>
        <FieldGroup>
          <Field data-invalid={Boolean(error) || undefined}>
            <FieldLabel htmlFor="password">{en.auth.newPassword}</FieldLabel>
            <PasswordInput
              id="password"
              name="password"
              autoComplete="new-password"
              minLength={6}
              required
              aria-invalid={Boolean(error) || undefined}
              aria-describedby={error ? 'reset-hint reset-error' : 'reset-hint'}
            />
            <FieldDescription id="reset-hint">{en.auth.passwordPolicy}</FieldDescription>
          </Field>
          {error ? <FieldError id="reset-error">{error}</FieldError> : null}
          {invalidLink ? <Link to="/forgot" className="text-sm font-medium underline underline-offset-4">{uiFlows.auth.requestNewLink}</Link> : null}
          <Button type="submit" disabled={pending}>
            {pending ? <LoaderCircleIcon data-icon="inline-start" className="animate-spin" /> : null}
            {pending ? uiFlows.auth.resetting : en.auth.resetPassword}
          </Button>
        </FieldGroup>
      </form>
      <p className="text-sm text-muted-foreground"><Link to="/login" className="font-medium text-foreground underline-offset-4 hover:underline">{uiFlows.auth.backToSignIn}</Link></p>
    </AuthScreen>
  )
}
