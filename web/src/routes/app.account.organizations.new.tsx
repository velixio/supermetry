import { useState, type FormEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'

import { Button } from '@/components/ui/button'
import { Field, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { createOrganization } from '@/features/organizations/api'

export const Route = createFileRoute('/app/account/organizations/new')({ component: NewOrganizationRoute })

function NewOrganizationRoute() {
  const navigate = Route.useNavigate()
  const queryClient = useQueryClient()
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const name = String(new FormData(event.currentTarget).get('name') ?? '').trim()
    setSubmitting(true)
    setError('')
    try {
      const created = await createOrganization(name)
      await queryClient.invalidateQueries({ queryKey: ['me'] })
      await navigate({ to: '/app/$org/sites/new', params: { org: created.slug } })
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The organization could not be created.')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="mx-auto flex w-full max-w-xl flex-col gap-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">Add organization</h1>
        <p className="text-sm text-muted-foreground">You will be its owner. Add its first site next.</p>
      </header>
      <section className="rounded-xl border bg-card p-5">
        <form className="flex flex-col gap-5" onSubmit={(event) => void submit(event)}>
          <FieldGroup>
            <Field data-invalid={Boolean(error) || undefined}>
              <FieldLabel htmlFor="organization-name">Organization name</FieldLabel>
              <Input id="organization-name" name="name" autoComplete="organization" maxLength={80} required aria-invalid={Boolean(error) || undefined} />
              <FieldError>{error}</FieldError>
            </Field>
          </FieldGroup>
          <Button type="submit" disabled={submitting} className="self-start">
            {submitting ? 'Creating…' : 'Add organization'}
          </Button>
        </form>
      </section>
    </div>
  )
}
