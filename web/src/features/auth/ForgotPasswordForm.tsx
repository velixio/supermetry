import { type FormEvent, useEffect, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { LoaderCircleIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Field, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { en } from '@/messages/en'
import { uiFlows } from '@/messages/uiFlows'
import { AuthScreen } from './AuthScreen'
import type { LoginResult } from './api'

const cooldownSeconds = 60

/**
 * Requests a password reset link. The reply never says whether the address has an account,
 * so the form cannot be used to discover who uses Supermetry.
 */
export function ForgotPasswordForm({ request, cooldown = cooldownSeconds }: { request: (email: string) => Promise<LoginResult>; cooldown?: number }) {
  const [email, setEmail] = useState('')
  const [pending, setPending] = useState(false)
  const [sentTo, setSentTo] = useState('')
  const [error, setError] = useState('')
  const [wait, setWait] = useState(0)

  useEffect(() => {
    if (wait <= 0) return
    const timer = setTimeout(() => setWait((value) => value - 1), 1_000)
    return () => clearTimeout(timer)
  }, [wait])

  async function submit(event?: FormEvent<HTMLFormElement>) {
    event?.preventDefault()
    if (pending || wait > 0) return
    setPending(true)
    setError('')
    try {
      const result = await request(email.trim())
      if (!result.ok && result.problem?.status === 429) {
        setError(en.auth.rateLimited)
      } else if (!result.ok && (result.problem?.status ?? 0) >= 500) {
        setError(en.auth.serverError)
      } else {
        setSentTo(email.trim())
        setWait(cooldown)
      }
    } catch {
      setError(en.auth.networkError)
    } finally {
      setPending(false)
    }
  }

  return (
    <AuthScreen title={uiFlows.auth.forgotHeading} description={uiFlows.auth.forgotDescription}>
      {sentTo ? (
        <div className="flex flex-col gap-3">
          <p role="status" className="rounded-lg border bg-muted/40 p-3 text-sm">{uiFlows.auth.forgotSent(sentTo)}</p>
          <p className="text-sm text-muted-foreground">{uiFlows.auth.forgotNoMail}</p>
          <Button type="button" variant="outline" disabled={pending || wait > 0} onClick={() => void submit()}>
            {pending ? <LoaderCircleIcon data-icon="inline-start" className="animate-spin" /> : null}
            {pending ? uiFlows.auth.sendingLink : uiFlows.auth.sendAgain}
          </Button>
          {wait > 0 ? <p className="text-xs text-muted-foreground" aria-live="off">{uiFlows.auth.resendWait(wait)}</p> : null}
          {error ? <FieldError>{error}</FieldError> : null}
        </div>
      ) : (
        <form onSubmit={(event) => void submit(event)}>
          <FieldGroup>
            <Field data-invalid={Boolean(error) || undefined}>
              <FieldLabel htmlFor="forgot-email">{en.auth.email}</FieldLabel>
              <Input
                id="forgot-email"
                name="email"
                type="email"
                autoComplete="email"
                spellCheck={false}
                autoCapitalize="none"
                required
                value={email}
                aria-invalid={Boolean(error) || undefined}
                aria-describedby={error ? 'forgot-error' : undefined}
                onChange={(event) => setEmail(event.target.value)}
              />
            </Field>
            {error ? <FieldError id="forgot-error">{error}</FieldError> : null}
            <Button type="submit" disabled={pending}>
              {pending ? <LoaderCircleIcon data-icon="inline-start" className="animate-spin" /> : null}
              {pending ? uiFlows.auth.sendingLink : uiFlows.auth.sendLink}
            </Button>
          </FieldGroup>
        </form>
      )}
      <p className="text-sm text-muted-foreground"><Link to="/login" className="font-medium text-foreground underline-offset-4 hover:underline">{uiFlows.auth.backToSignIn}</Link></p>
    </AuthScreen>
  )
}
