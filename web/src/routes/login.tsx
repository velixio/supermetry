import { useEffect, useState } from 'react'
import { createFileRoute, redirect, useRouter } from '@tanstack/react-router'

import { LoginForm } from '@/features/auth/LoginForm'
import { createAuthConfigApi, postPrivate, type SignupMode } from '@/features/auth/api'
import { safeAuthNext } from '@/features/auth/navigation'
import { resetAfterLogin } from '@/lib/session-lifecycle'
import { applyPreferredTheme } from '@/lib/theme'

const parseLoginSearch = (value: unknown): { next?: string } => {
  const search = value && typeof value === 'object' ? value as Record<string, unknown> : {}
  return typeof search.next === 'string' ? { next: search.next } : {}
}

type LoginMembership = { org: { slug: string } }

function targetOrganization(target: string): string | undefined {
  const match = /^\/app\/([^/]+)(?:\/|$)/.exec(new URL(target, window.location.origin).pathname)
  if (!match || match[1] === 'account') return undefined
  try { return decodeURIComponent(match[1]) } catch { return match[1] }
}

function permittedTarget(target: string, memberships: LoginMembership[]): string {
  const org = targetOrganization(target)
  return !org || memberships.some((membership) => membership.org.slug === org) ? target : '/app'
}

async function signedInTarget(target: string, fetchMe: typeof globalThis.fetch = (...args) => globalThis.fetch(...args)): Promise<string | null> {
  try {
    const response = await fetchMe('/api/private/me', { credentials: 'same-origin', headers: { Accept: 'application/json' } })
    if (!response.ok) return null
    const body = await response.json() as { user?: unknown; memberships?: LoginMembership[] }
    if (!body?.user || !Array.isArray(body.memberships)) return null
    return permittedTarget(target, body.memberships)
  } catch { return null }
}

export const Route = createFileRoute('/login')({
  validateSearch: parseLoginSearch,
  beforeLoad: async ({ search }) => {
    const target = await signedInTarget(safeAuthNext(search.next ?? '/app'))
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
    void createAuthConfigApi()().then((config) => { if (active) setSignup(config.signup) }).catch(() => { if (active) setSignup('invite') })
    return () => { active = false }
  }, [])

  return <LoginForm
    login={(credentials) => postPrivate('/api/private/auth/login', credentials)}
    next={safeAuthNext(search.next ?? '/app')}
    onSuccess={async (next) => {
      resetAfterLogin()
      applyPreferredTheme()
      if (next.startsWith('/api/')) window.location.assign(next)
      else router.history.replace((await signedInTarget(next)) ?? '/app')
    }}
    signup={signup}
  />
}
