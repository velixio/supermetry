import { type FormEvent, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { LoaderCircleIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { en } from '@/messages/en'
import { AuthScreen } from './AuthScreen'
import type { LoginCredentials, LoginResult, SignupMode } from './api'
import { authProblemMessage } from './api'
import { safeAuthNext } from './navigation'

export function LoginForm({ login, next, onSuccess, signup }: {
  login: (credentials: LoginCredentials) => Promise<LoginResult>
  next: string
  onSuccess: (next: string) => void
  signup?: SignupMode
}) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [totp, setTotp] = useState('')
  const [needsTOTP, setNeedsTOTP] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setPending(true)
    setError('')
    try {
      const result = await login({ email, password, ...(needsTOTP ? { totp } : {}) })
      if (result.ok) onSuccess(safeAuthNext(next))
      else if ((result.problem?.type ?? result.type)?.endsWith('totp-required')) setNeedsTOTP(true)
      else setError(result.type?.endsWith('/password-policy') ? en.auth.passwordPolicy : authProblemMessage(result.problem))
    } catch (reason) { setError(reason instanceof TypeError ? en.auth.networkError : en.auth.serverError) }
    finally { setPending(false) }
  }

  return <AuthScreen title={en.auth.signInHeading} description={needsTOTP ? en.auth.totpDescription : en.auth.signInDescription}>
    <form onSubmit={submit}><FieldGroup>
      {!needsTOTP ? <>
        <Field><FieldLabel htmlFor="email">{en.auth.email}</FieldLabel><Input id="email" name="email" type="email" autoComplete="email" required value={email} onChange={(event) => setEmail(event.target.value)} /></Field>
        <Field><FieldLabel htmlFor="password">{en.auth.password}</FieldLabel><Input id="password" name="password" type="password" autoComplete="current-password" required value={password} onChange={(event) => setPassword(event.target.value)} /></Field>
      </> : <Field><FieldLabel htmlFor="totp">{en.auth.authenticationCode}</FieldLabel><Input id="totp" name="totp" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" required value={totp} onChange={(event) => setTotp(event.target.value)} /><FieldDescription>{en.auth.totpDescription}</FieldDescription></Field>}
      {error ? <FieldError>{error}</FieldError> : null}
      <Button type="submit" disabled={pending}>{pending ? <LoaderCircleIcon data-icon="inline-start" /> : null}{needsTOTP ? pending ? en.auth.verifying : en.auth.verify : pending ? en.auth.signingIn : en.auth.signIn}</Button>
    </FieldGroup></form>
    {signup === 'open' ? <p className="text-sm text-muted-foreground">New to Supermetry? <Link to="/signup">Create an account</Link></p> : null}
  </AuthScreen>
}
