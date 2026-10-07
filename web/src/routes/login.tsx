import { type FormEvent, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { LoaderCircleIcon } from 'lucide-react'
import { z } from 'zod'

import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { createApi } from '@/lib/api'
import { en } from '@/messages/en'

const loginSearch = z.object({ next: z.string().optional().catch(undefined) })

export type LoginCredentials = {
  email: string
  password: string
  totp?: string
}

export type LoginResult = {
  ok: boolean
  type?: string
}

type AuthClient = {
  POST: (
    path: string,
    options: { body: Record<string, string> },
  ) => Promise<{ data?: unknown; error?: unknown; response: Response }>
}

type Problem = { type?: string; status?: number }

function problemType(value: unknown): string | undefined {
  if (!value || typeof value !== 'object') return undefined
  const type = (value as Problem).type
  if (!type) return undefined
  return type.split('/').pop()
}

export function createAuthApi(fetch?: typeof globalThis.fetch) {
  const client = createApi({ fetch, onUnauthorized: () => undefined }) as unknown as AuthClient
  return async (path: string, body: Record<string, string>): Promise<LoginResult> => {
    const result = await client.POST(path, { body })
    return result.response.ok
      ? { ok: true }
      : { ok: false, type: problemType(result.error) }
  }
}

const authApi = createAuthApi()

export async function postPrivate(path: string, body: Record<string, string>) {
  return authApi(path, body)
}

export async function authResultMessage(
  operation: () => Promise<LoginResult>,
): Promise<string> {
  try {
    const result = await operation()
    return result.ok ? en.auth.success : en.auth.genericError
  } catch {
    return en.auth.genericError
  }
}

function safeNext(next: string): string {
  if (!next.startsWith('/') || next.startsWith('//') || /[\\\r\n]/.test(next)) {
    return '/'
  }
  const parsed = new URL(next, window.location.origin)
  if (parsed.origin !== window.location.origin) return '/'
  return `${parsed.pathname}${parsed.search}${parsed.hash}`
}

export const Route = createFileRoute('/login')({
  validateSearch: (search) => loginSearch.parse(search),
  component: LoginPage,
})

function LoginPage() {
  const search = Route.useSearch()
  return (
    <LoginForm
      login={(credentials) =>
        postPrivate('/api/private/auth/login', credentials)
      }
      next={safeNext(search.next ?? '/')}
      onSuccess={(next) => window.location.assign(next)}
    />
  )
}

export function AuthScreen({
  title,
  description,
  children,
}: {
  title: string
  description: string
  children: React.ReactNode
}) {
  return (
    <main className="grid min-h-dvh place-items-center px-5 py-12">
      <section className="flex w-full max-w-sm flex-col gap-8">
        <header className="flex flex-col gap-2 border-l-2 border-accent pl-5">
          <p className="text-sm font-medium text-muted-foreground">{en.appName}</p>
          <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
          <p className="text-sm leading-6 text-muted-foreground">{description}</p>
        </header>
        {children}
      </section>
    </main>
  )
}

export function LoginForm({
  login,
  next,
  onSuccess,
}: {
  login: (credentials: LoginCredentials) => Promise<LoginResult>
  next: string
  onSuccess: (next: string) => void
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
      const result = await login({
        email,
        password,
        ...(needsTOTP ? { totp } : {}),
      })
      if (result.ok) {
        onSuccess(safeNext(next))
      } else if (result.type === 'totp-required') {
        setNeedsTOTP(true)
      } else {
        setError(en.auth.genericError)
      }
    } catch {
      setError(en.auth.genericError)
    } finally {
      setPending(false)
    }
  }

  return (
    <AuthScreen
      title={en.auth.signInHeading}
      description={needsTOTP ? en.auth.totpDescription : en.auth.signInDescription}
    >
      <form onSubmit={submit}>
        <FieldGroup>
          {!needsTOTP ? (
            <>
              <Field>
                <FieldLabel htmlFor="email">{en.auth.email}</FieldLabel>
                <Input
                  id="email"
                  name="email"
                  type="email"
                  autoComplete="email"
                  required
                  value={email}
                  onChange={(event) => setEmail(event.target.value)}
                />
              </Field>
              <Field>
                <FieldLabel htmlFor="password">{en.auth.password}</FieldLabel>
                <Input
                  id="password"
                  name="password"
                  type="password"
                  autoComplete="current-password"
                  minLength={12}
                  required
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                />
              </Field>
            </>
          ) : (
            <Field>
              <FieldLabel htmlFor="totp">{en.auth.authenticationCode}</FieldLabel>
              <Input
                id="totp"
                name="totp"
                inputMode="numeric"
                autoComplete="one-time-code"
                pattern="[0-9]{6}"
                required
                value={totp}
                onChange={(event) => setTotp(event.target.value)}
              />
              <FieldDescription>{en.auth.totpDescription}</FieldDescription>
            </Field>
          )}
          {error ? <FieldError>{error}</FieldError> : null}
          <Button type="submit" disabled={pending}>
            {pending ? <LoaderCircleIcon data-icon="inline-start" /> : null}
            {needsTOTP
              ? pending
                ? en.auth.verifying
                : en.auth.verify
              : pending
                ? en.auth.signingIn
                : en.auth.signIn}
          </Button>
        </FieldGroup>
      </form>
    </AuthScreen>
  )
}
