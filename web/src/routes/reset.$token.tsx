import { type FormEvent, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'

import { Button } from '@/components/ui/button'
import { Field, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { en } from '@/messages/en'

import { AuthScreen, authResultMessage, postPrivate } from './login'

export const Route = createFileRoute('/reset/$token')({
  component: ResetPage,
})

function ResetPage() {
  const { token } = Route.useParams()
  const [message, setMessage] = useState('')

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    setMessage(
      await authResultMessage(() =>
        postPrivate('/api/private/auth/password-reset/confirm', {
          token,
          password: String(form.get('password') ?? ''),
        }),
      ),
    )
  }

  return (
    <AuthScreen title={en.auth.resetHeading} description={en.auth.resetDescription}>
      <form onSubmit={submit}>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="password">{en.auth.newPassword}</FieldLabel>
            <Input
              id="password"
              name="password"
              type="password"
              autoComplete="new-password"
              minLength={12}
              required
            />
          </Field>
          {message ? <FieldError>{message}</FieldError> : null}
          <Button type="submit">{en.auth.resetPassword}</Button>
        </FieldGroup>
      </form>
    </AuthScreen>
  )
}
