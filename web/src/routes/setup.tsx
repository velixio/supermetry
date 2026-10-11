import { type FormEvent, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { en } from '@/messages/en'

import { AuthScreen, authResultMessage, postPrivate } from './login'

const setupSearch = z.object({ token: z.string().optional().catch(undefined) })

export const Route = createFileRoute('/setup')({
  validateSearch: (search) => setupSearch.parse(search),
  component: SetupPage,
})

function SetupPage() {
  const { token = '' } = Route.useSearch()
  const [message, setMessage] = useState('')

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    setMessage(
      await authResultMessage(() =>
        postPrivate('/api/private/setup', {
          token,
          email: String(form.get('email') ?? ''),
          name: String(form.get('name') ?? ''),
          password: String(form.get('password') ?? ''),
          org_name: String(form.get('org_name') ?? ''),
        }),
      ),
    )
  }

  return (
    <AuthScreen title={en.auth.setupHeading} description={en.auth.setupDescription}>
      <form onSubmit={submit}>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="name">{en.auth.name}</FieldLabel>
            <Input id="name" name="name" autoComplete="name" required />
          </Field>
          <Field>
            <FieldLabel htmlFor="email">{en.auth.email}</FieldLabel>
            <Input id="email" name="email" type="email" autoComplete="email" required />
          </Field>
          <Field>
            <FieldLabel htmlFor="org_name">{en.auth.organizationName}</FieldLabel>
            <Input id="org_name" name="org_name" required />
          </Field>
          <Field>
            <FieldLabel htmlFor="password">{en.auth.password}</FieldLabel>
			<Input id="password" name="password" type="password" minLength={6} required />
			<FieldDescription>{en.auth.passwordPolicy}</FieldDescription>
          </Field>
          {message ? <FieldError>{message}</FieldError> : null}
          <Button type="submit">{en.auth.completeSetup}</Button>
        </FieldGroup>
      </form>
    </AuthScreen>
  )
}
