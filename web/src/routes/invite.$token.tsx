import { type FormEvent, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { en } from '@/messages/en'

import { AuthScreen, authResultMessage, postPrivate } from './login'

export const Route = createFileRoute('/invite/$token')({
  component: InvitationPage,
})

function InvitationPage() {
  const { token } = Route.useParams()
  const [message, setMessage] = useState('')

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    setMessage(
      await authResultMessage(() =>
        postPrivate(
          `/api/private/invitations/${encodeURIComponent(token)}/accept`,
          {
            name: String(form.get('name') ?? ''),
            password: String(form.get('password') ?? ''),
          },
        ),
      ),
    )
  }

  return (
    <AuthScreen
      title={en.auth.invitationHeading}
      description={en.auth.invitationDescription}
    >
      <form onSubmit={submit}>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="name">{en.auth.name}</FieldLabel>
            <Input id="name" name="name" autoComplete="name" required />
          </Field>
          <Field>
            <FieldLabel htmlFor="password">{en.auth.password}</FieldLabel>
			<Input id="password" name="password" type="password" minLength={6} required />
			<FieldDescription>{en.auth.passwordPolicy}</FieldDescription>
          </Field>
          {message ? <FieldError>{message}</FieldError> : null}
          <Button type="submit">{en.auth.acceptInvitation}</Button>
        </FieldGroup>
      </form>
    </AuthScreen>
  )
}
