import { createFileRoute, redirect } from '@tanstack/react-router'

import { ForgotPasswordForm } from '@/features/auth/ForgotPasswordForm'
import { postPrivate } from '@/features/auth/api'
import { signedInRedirect } from '@/lib/signed-in'

export const Route = createFileRoute('/forgot')({
  beforeLoad: async () => {
    const target = await signedInRedirect('/app')
    if (target) throw redirect({ href: target, replace: true })
  },
  component: ForgotPasswordPage,
})

function ForgotPasswordPage() {
  return <ForgotPasswordForm request={(email) => postPrivate('/api/private/auth/password-reset', { email })} />
}
