import { type FormEvent, useEffect, useState } from 'react'
import { createFileRoute, Link, redirect, useRouter } from '@tanstack/react-router'
import { LoaderCircleIcon } from 'lucide-react'
import { z } from 'zod'

import { Button } from '@/components/ui/button'
import { PoweredBySupermetry } from '@/components/PoweredBySupermetry'
import { BrandLogo } from '@/components/BrandLogo'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { createApi } from '@/lib/api'
import { resetAfterLogin } from '@/lib/session-lifecycle'
import { applyPreferredTheme } from '@/lib/theme'
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

export type SignupMode = 'open' | 'invite'

export type AuthConfig = {
  signup: SignupMode
  setup_required: boolean
  homepage_url: string
}

type AuthClient = {
  GET: (
    path: string,
  ) => Promise<{ data?: unknown; error?: unknown; response: Response }>
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

export function createAuthConfigApi(fetch?: typeof globalThis.fetch) {
  const client = createApi({ fetch, onUnauthorized: () => undefined }) as unknown as AuthClient
  return async (): Promise<AuthConfig> => {
    const result = await client.GET('/api/private/auth/config')
    if (!result.response.ok || !result.data) {
      throw new Error('Authentication configuration is unavailable')
    }
    return result.data as AuthConfig
  }
}

const authConfigApi = createAuthConfigApi()

export async function postPrivate(path: string, body: Record<string, string>) {
  return createAuthApi()(path, body)
}

export async function authResultMessage(
  operation: () => Promise<LoginResult>,
): Promise<string> {
  try {
    const result = await operation()
    if (result.ok) return en.auth.success
    return result.type === 'password-policy' ? en.auth.passwordPolicy : en.auth.genericError
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

type LoginMembership = { org: { slug: string } }

function targetOrganization(target: string): string | undefined {
  const pathname = new URL(target, window.location.origin).pathname
  const match = /^\/app\/([^/]+)(?:\/|$)/.exec(pathname)
  if (!match || match[1] === 'account') return undefined
  try {
    return decodeURIComponent(match[1])
  } catch {
    return match[1]
  }
}

function permittedTarget(target: string, memberships: LoginMembership[]): string {
  const org = targetOrganization(target)
  return !org || memberships.some((membership) => membership.org.slug === org)
    ? target
    : '/app'
}

async function signedInTarget(
  target: string,
  fetchMe: typeof globalThis.fetch = (...args) => globalThis.fetch(...args),
): Promise<string | null> {
  try {
    const response = await fetchMe('/api/private/me', {
      credentials: 'same-origin',
      headers: { Accept: 'application/json' },
    })
    if (!response.ok) return null
    const body = (await response.json()) as {
      user?: unknown
      memberships?: LoginMembership[]
    }
    if (!body?.user || !Array.isArray(body.memberships)) return null
    return permittedTarget(target, body.memberships)
  } catch {
    return null
  }
}

export const Route = createFileRoute('/login')({
  validateSearch: (search) => loginSearch.parse(search),
  beforeLoad: async ({ search }) => {
    const target = await signedInTarget(safeNext(search.next ?? '/app'))
    if (target) throw redirect({ href: target, replace: true })
  },
  component: LoginPage,
})

function LoginPage() {
  const search = Route.useSearch()
  const router = useRouter()
  const [signup, setSignup] = useState<SignupMode>()

  useEffect(() => {
    let active = true
    void authConfigApi()
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
    <LoginForm
      login={(credentials) =>
        postPrivate('/api/private/auth/login', credentials)
      }
      next={safeNext(search.next ?? '/app')}
      onSuccess={async (next) => {
        resetAfterLogin()
        applyPreferredTheme()
        // Server routes (such as the emailed email-change link) need a full load.
        if (next.startsWith('/api/')) window.location.assign(next)
        else router.history.replace((await signedInTarget(next)) ?? '/app')
      }}
      signup={signup}
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
    <main className="dark grid min-h-dvh place-items-center bg-background px-5 py-12 text-foreground">
      <section className="flex w-full max-w-sm flex-col gap-8">
        <header className="flex flex-col gap-2 border-l-2 border-accent pl-5">
          <BrandLogo className="h-7 w-auto self-start" />
          <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
          <p className="text-sm leading-6 text-muted-foreground">{description}</p>
        </header>
        {children}
        <PoweredBySupermetry className="text-xs text-muted-foreground hover:text-foreground" />
      </section>
    </main>
  )
}

export function LoginForm({
  login,
  next,
  onSuccess,
  signup,
}: {
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
      {signup === 'open' ? (
        <p className="text-sm text-muted-foreground">
          New to Supermetry? <Link to="/signup">Create an account</Link>
        </p>
      ) : null}
    </AuthScreen>
  )
}
