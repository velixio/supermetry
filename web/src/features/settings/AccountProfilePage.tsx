import { type FormEvent, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { privateRequest } from './privateApi'

type Method = 'PATCH' | 'POST' | 'DELETE'
type Request = (method: Method, path: string, body: Record<string, string>) => Promise<unknown>

export function AccountProfilePage({ name, email, totpEnabled, request = (method, path, body) => privateRequest(method, path, { body }), onDeleted }: {
  name: string; email: string; totpEnabled: boolean; request?: Request; onDeleted: () => void
}) {
  const queryClient = useQueryClient()
  const [deleteBody, setDeleteBody] = useState<Record<string, string> | null>(null)
  const [emailSent, setEmailSent] = useState(false)
  const mutation = useMutation({
    mutationFn: ({ method, path, body }: { method: Method; path: string; body: Record<string, string> }) => request(method, path, body),
    onSuccess: (_, variables) => {
      if (variables.method === 'PATCH') void queryClient.invalidateQueries({ queryKey: ['me'] })
      if (variables.path.endsWith('/email')) setEmailSent(true)
      if (variables.method === 'DELETE') onDeleted()
    },
  })
  const submit = (method: Method, path: string, fields: string[]) => (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    mutation.mutate({ method, path, body: Object.fromEntries(fields.map((field) => [field, String(data.get(field) ?? '')])) })
  }
  return <div className="mx-auto flex w-full max-w-3xl flex-col gap-6">
    <header><h1 className="text-2xl font-semibold tracking-tight">Profile</h1><p className="text-sm text-muted-foreground">Manage your identity and account credentials.</p></header>
    <Card><CardHeader><CardTitle>Display name</CardTitle><CardDescription>Shown across every organization.</CardDescription></CardHeader>
      <form onSubmit={submit('PATCH', '/api/private/me', ['name'])}><CardContent><FieldGroup><Field><FieldLabel htmlFor="display-name">Display name</FieldLabel><Input id="display-name" name="name" defaultValue={name} required /></Field></FieldGroup></CardContent><CardFooter><Button disabled={mutation.isPending}>Save name</Button></CardFooter></form></Card>
    <Card><CardHeader><CardTitle>Email address</CardTitle><CardDescription>We will send a one-hour confirmation link to the new address. Your current address is {email}.</CardDescription></CardHeader>
      <form onSubmit={submit('POST', '/api/private/me/email', ['new_email', 'password'])}><CardContent><FieldGroup><Field><FieldLabel htmlFor="new-email">New email</FieldLabel><Input id="new-email" name="new_email" type="email" required /></Field><Field><FieldLabel htmlFor="email-password">Email change password</FieldLabel><Input id="email-password" name="password" type="password" required /></Field>{emailSent ? <FieldDescription>Check the new address for a confirmation link.</FieldDescription> : null}</FieldGroup></CardContent><CardFooter><Button disabled={mutation.isPending}>Send confirmation</Button></CardFooter></form></Card>
    <Card><CardHeader><CardTitle>Password</CardTitle><CardDescription>Changing it signs out your other sessions.</CardDescription></CardHeader>
      <form onSubmit={submit('POST', '/api/private/me/password', ['current_password', 'new_password'])}><CardContent><FieldGroup><Field><FieldLabel htmlFor="current-password">Current password</FieldLabel><Input id="current-password" name="current_password" type="password" required /></Field><Field><FieldLabel htmlFor="new-password">New password</FieldLabel><Input id="new-password" name="new_password" type="password" minLength={6} required /></Field></FieldGroup></CardContent><CardFooter><Button disabled={mutation.isPending}>Change password</Button></CardFooter></form></Card>
    <Card><CardHeader><CardTitle>Delete account</CardTitle><CardDescription>Your identity is anonymised and memberships and sessions are removed. You must transfer ownership or delete organizations where you are the last owner.</CardDescription></CardHeader>
      <form onSubmit={(event) => { event.preventDefault(); const data = new FormData(event.currentTarget); const body: Record<string, string> = { password: String(data.get('password') ?? '') }; if (totpEnabled) body.totp_code = String(data.get('totp_code') ?? ''); setDeleteBody(body) }}><CardContent><FieldGroup><Field><FieldLabel htmlFor="delete-password">Delete account password</FieldLabel><Input id="delete-password" name="password" type="password" required /></Field>{totpEnabled ? <Field><FieldLabel htmlFor="delete-totp">Authentication code</FieldLabel><Input id="delete-totp" name="totp_code" inputMode="numeric" required /></Field> : null}{mutation.error ? <FieldError>{mutation.error.message}</FieldError> : null}</FieldGroup></CardContent><CardFooter><Button variant="destructive">Delete account</Button></CardFooter></form></Card>
    <AlertDialog open={deleteBody !== null} onOpenChange={(open) => { if (!open) setDeleteBody(null) }}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>Delete your account?</AlertDialogTitle><AlertDialogDescription>This cannot be undone. Your account identity will be anonymised.</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel>Cancel</AlertDialogCancel><AlertDialogAction variant="destructive" disabled={mutation.isPending} onClick={(event) => { event.preventDefault(); if (deleteBody) mutation.mutate({ method: 'DELETE', path: '/api/private/me', body: deleteBody }) }}>Permanently delete account</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog>
  </div>
}
