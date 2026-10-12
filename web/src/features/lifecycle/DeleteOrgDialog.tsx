import { useState, type FormEvent } from 'react'
import { useMutation } from '@tanstack/react-query'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Field, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { deleteOrganization, type DeletionResult, type SensitiveAction } from './api'
import { activeLocale } from '@/lib/i18n/intl'

function localDate(value: string): string {
  return new Intl.DateTimeFormat(activeLocale(), { dateStyle: 'medium' }).format(new Date(value))
}

export function DeleteOrgDialog({
  orgId,
  orgName,
  hasNonDemoSites,
  totpEnabled,
  remove = deleteOrganization,
  navigate,
}: {
  orgId: number
  orgName: string
  hasNonDemoSites: boolean
  totpEnabled: boolean
  remove?: (orgId: number, body: SensitiveAction) => Promise<DeletionResult>
  navigate: (path: '/app' | '/app/account') => void
}) {
  const [open, setOpen] = useState(false)
  const mutation = useMutation({
    mutationFn: (body: SensitiveAction) => remove(orgId, body),
    onSuccess: (result) => {
      setOpen(false)
      if (result.scheduled && result.purge_at) {
        toast.success(`Organization scheduled for deletion on ${localDate(result.purge_at)}`)
        navigate('/app/account')
        return
      }
      navigate('/app')
    },
  })

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    const code = String(data.get('totp_code') ?? '')
    mutation.mutate({
      password: String(data.get('password') ?? ''),
      ...(code ? { totp_code: code } : {}),
    })
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Danger zone</CardTitle>
        <CardDescription>Delete this organization and its projects.</CardDescription>
      </CardHeader>
      <CardContent>
        <p className="text-sm font-medium">{orgName}</p>
      </CardContent>
      <CardFooter>
        <Dialog open={open} onOpenChange={(next) => { setOpen(next); if (!next) mutation.reset() }}>
          <DialogTrigger asChild><Button variant="destructive">Delete organization</Button></DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Delete {orgName}?</DialogTitle>
              <DialogDescription>
                {hasNonDemoSites
                  ? 'The organization stops collecting data now. You can restore it for 7 days; after that its data is permanently deleted.'
                  : 'The organization and any demo project data are deleted now.'}
              </DialogDescription>
            </DialogHeader>
            <form className="flex flex-col gap-4" onSubmit={submit}>
              <FieldGroup>
                <Field>
                  <FieldLabel htmlFor="delete-organization-password">Current password</FieldLabel>
                  <Input id="delete-organization-password" name="password" type="password" autoComplete="current-password" required />
                </Field>
                {totpEnabled ? (
                  <Field>
                    <FieldLabel htmlFor="delete-organization-totp">Authentication code</FieldLabel>
                    <Input id="delete-organization-totp" name="totp_code" inputMode="numeric" autoComplete="one-time-code" required />
                  </Field>
                ) : null}
                {mutation.error ? <FieldError>{mutation.error.message}</FieldError> : null}
              </FieldGroup>
              <DialogFooter>
                <DialogClose asChild><Button type="button" variant="outline">Cancel</Button></DialogClose>
                <Button type="submit" variant="destructive" disabled={mutation.isPending}>Confirm deletion</Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>
      </CardFooter>
    </Card>
  )
}
