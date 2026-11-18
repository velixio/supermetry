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
  // setup=1 marks the first-password link of a newly created account.
  validateSearch: (search: Record<string, unknown>): { setup?: boolean } =>
    search.setup === 1 || search.setup === '1' || search.setup === true ? { setup: true } : {},
  component: ResetRoute,
})

function ResetRoute() {
  const { token } = Route.useParams()
  const { setup } = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <ResetPasswordForm
      setup={setup === true}
      submit={(password) => postPrivate('/api/private/auth/password-reset/confirm', { token, password, sign_in: true })}
      onDone={(signedIn) => void (signedIn
        ? navigate({ to: '/app', replace: true })
        : navigate({ to: '/login', search: { notice: 'reset' }, replace: true }))}
    />
  )
}

export function ResetPasswordForm({ submit, onDone, setup = false }: { submit: (password: string) => Promise<LoginResult>; onDone: (signedIn: boolean) => void; setup?: boolean }) {
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
        onDone(result.signedIn === true)
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
    <AuthScreen title={setup ? en.auth.accountSetupHeading : en.auth.resetHeading} description={setup ? en.auth.accountSetupDescription : en.auth.resetDescription}>
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
            {pending ? (setup ? en.auth.settingPassword : uiFlows.auth.resetting) : (setup ? en.auth.setPassword : en.auth.resetPassword)}
          </Button>
        </FieldGroup>
      </form>
      <p className="text-sm text-muted-foreground"><Link to="/login" className="font-medium text-foreground underline-offset-4 hover:underline">{uiFlows.auth.backToSignIn}</Link></p>
    </AuthScreen>
  )
}
