import { useState, type FormEvent } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

import type { Role } from '@/components/Sidebar'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { renameOrganization, type Organization } from './api'

const controlCharacters = /[\u0000-\u001f\u007f]/

function validateName(value: string): string | undefined {
  if (!value.trim()) return 'Enter an organization name.'
  if ([...value.trim()].length > 100) return 'Use 100 characters or fewer.'
  if (controlCharacters.test(value)) return 'Remove control characters from the name.'
  return undefined
}

function renameError(error: unknown): string {
  if (error instanceof Error && error.message === 'invalid_name') return 'Enter a valid organization name.'
  return 'The organization could not be renamed.'
}

export function OrgNameForm({
  orgId,
  name,
  role,
  rename = renameOrganization,
}: {
  orgId: number
  name: string
  role: Role
  rename?: (orgId: number, name: string) => Promise<Organization>
}) {
  const queryClient = useQueryClient()
  const [value, setValue] = useState(name)
  const [error, setError] = useState<string>()
  const mutation = useMutation({
    mutationFn: (nextName: string) => rename(orgId, nextName),
    onSuccess: async () => {
      toast.success('Organization renamed')
      await queryClient.invalidateQueries({ queryKey: ['me'] })
    },
  })
  const canRename = role === 'owner' || role === 'admin'

  if (!canRename) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Name</CardTitle>
          <CardDescription>Organization names are managed by owners and admins.</CardDescription>
        </CardHeader>
        <CardContent><p className="text-sm font-medium">{name}</p></CardContent>
      </Card>
    )
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const validation = validateName(value)
    setError(validation)
    if (validation) return
    mutation.mutate(value.trim(), {
      onError: (reason) => setError(renameError(reason)),
    })
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Name</CardTitle>
        <CardDescription>This name appears in the organization switcher.</CardDescription>
      </CardHeader>
      <form onSubmit={submit}>
        <CardContent>
          <FieldGroup>
            <Field data-invalid={Boolean(error)}>
              <FieldLabel htmlFor="organization-name">Name</FieldLabel>
              <Input
                id="organization-name"
                value={value}
                aria-invalid={Boolean(error)}
                onChange={(event) => {
                  setValue(event.target.value)
                  setError(undefined)
                }}
              />
              {error ? <FieldError>{error}</FieldError> : null}
            </Field>
          </FieldGroup>
        </CardContent>
        <CardFooter className="pt-4">
          <Button disabled={mutation.isPending || value.trim() === name}>Save changes</Button>
        </CardFooter>
      </form>
    </Card>
  )
}
