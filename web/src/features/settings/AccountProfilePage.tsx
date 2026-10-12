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
import type { UserPreferences } from '@/lib/me'
import { applyLocalePreference, manifest, pickerLocales, type LocaleCode } from '@/lib/i18n'
import { en } from '@/messages/en'
import { privateRequest } from './privateApi'
import { ui } from '@/messages/ui'

type Method = 'PATCH' | 'POST' | 'DELETE'
type Request = (method: Method, path: string, body: Record<string, unknown>) => Promise<unknown>

export function AccountProfilePage({ name, email, totpEnabled, preferences, request = (method, path, body) => privateRequest(method, path, { body }), onDeleted }: {
  name: string; email: string; totpEnabled: boolean; preferences?: UserPreferences; request?: Request; onDeleted: () => void
}) {
  const queryClient = useQueryClient()
  const [deleteBody, setDeleteBody] = useState<Record<string, unknown> | null>(null)
  const [emailSent, setEmailSent] = useState(false)
  const mutation = useMutation({
    mutationFn: ({ method, path, body }: { method: Method; path: string; body: Record<string, unknown> }) => request(method, path, body),
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
    <header><h1 className="text-2xl font-semibold tracking-tight">{ui.profile.title}</h1><p className="text-sm text-muted-foreground">{ui.profile.intro}</p></header>
    <Card><CardHeader><CardTitle>{ui.profile.displayName}</CardTitle><CardDescription>{ui.profile.displayNameNote}</CardDescription></CardHeader>
      <form onSubmit={submit('PATCH', '/api/private/me', ['name'])}><CardContent><FieldGroup><Field><FieldLabel htmlFor="display-name">{ui.profile.displayName}</FieldLabel><Input id="display-name" name="name" defaultValue={name} required /></Field></FieldGroup></CardContent><CardFooter><Button disabled={mutation.isPending}>{ui.profile.saveName}</Button></CardFooter></form></Card>
    <Card><CardHeader><CardTitle>{ui.profile.landingTitle}</CardTitle><CardDescription>{ui.profile.landingNote}</CardDescription></CardHeader>
      <CardContent><FieldGroup><Field><FieldLabel htmlFor="landing-page">{ui.profile.afterSignIn}</FieldLabel><select id="landing-page" className="h-8 rounded-lg border bg-background px-2.5 text-sm" defaultValue={preferences?.landing_page ?? 'overview'} disabled={mutation.isPending} onChange={(event) => mutation.mutate({ method: 'PATCH', path: '/api/private/me', body: { preferences: { landing_page: event.target.value } } })}><option value="overview">{ui.profile.sitesOverview}</option><option value="last_site">{ui.profile.lastSite}</option></select><FieldDescription>{ui.profile.landingHint}</FieldDescription></Field></FieldGroup></CardContent></Card>
    <Card><CardHeader><CardTitle>{en.language.title}</CardTitle><CardDescription>{en.language.description}</CardDescription></CardHeader>
      <CardContent><FieldGroup><Field><FieldLabel htmlFor="interface-language">{en.language.label}</FieldLabel><select id="interface-language" className="h-11 rounded-lg border bg-background px-2.5 text-sm" defaultValue={preferences?.locale ?? ''} disabled={mutation.isPending} onChange={(event) => { const value = event.target.value as LocaleCode | ''; mutation.mutate({ method: 'PATCH', path: '/api/private/me', body: { preferences: { locale: value } } }, { onSuccess: () => applyLocalePreference(value) }) }}><option value="">{en.language.browserDefault}</option>{pickerLocales.map((code) => <option key={code} value={code}>{manifest[code].nativeName}{manifest[code].reviewed ? '' : ` ${en.language.machineTranslated}`}</option>)}</select><FieldDescription>{en.language.note}</FieldDescription></Field></FieldGroup></CardContent></Card>
    <Card><CardHeader><CardTitle>{ui.profile.emailTitle}</CardTitle><CardDescription>{ui.profile.emailNote(email)}</CardDescription></CardHeader>
      <form onSubmit={submit('POST', '/api/private/me/email', ['new_email', 'password'])}><CardContent><FieldGroup><Field><FieldLabel htmlFor="new-email">{ui.profile.newEmail}</FieldLabel><Input id="new-email" name="new_email" type="email" required /></Field><Field><FieldLabel htmlFor="email-password">{ui.profile.emailPassword}</FieldLabel><Input id="email-password" name="password" type="password" required /></Field>{emailSent ? <FieldDescription>{ui.profile.emailSent}</FieldDescription> : null}</FieldGroup></CardContent><CardFooter><Button disabled={mutation.isPending}>{ui.profile.sendConfirmation}</Button></CardFooter></form></Card>
    <Card><CardHeader><CardTitle>{ui.profile.passwordTitle}</CardTitle><CardDescription>{ui.profile.passwordNote}</CardDescription></CardHeader>
      <form onSubmit={submit('POST', '/api/private/me/password', ['current_password', 'new_password'])}><CardContent><FieldGroup><Field><FieldLabel htmlFor="current-password">{ui.profile.currentPassword}</FieldLabel><Input id="current-password" name="current_password" type="password" required /></Field><Field><FieldLabel htmlFor="new-password">{ui.profile.newPassword}</FieldLabel><Input id="new-password" name="new_password" type="password" minLength={6} required /></Field></FieldGroup></CardContent><CardFooter><Button disabled={mutation.isPending}>{ui.profile.changePassword}</Button></CardFooter></form></Card>
    <Card><CardHeader><CardTitle>{ui.profile.deleteTitle}</CardTitle><CardDescription>{ui.profile.deleteNote}</CardDescription></CardHeader>
      <form onSubmit={(event) => { event.preventDefault(); const data = new FormData(event.currentTarget); const body: Record<string, unknown> = { password: String(data.get('password') ?? '') }; if (totpEnabled) body.totp_code = String(data.get('totp_code') ?? ''); setDeleteBody(body) }}><CardContent><FieldGroup><Field><FieldLabel htmlFor="delete-password">{ui.profile.deletePassword}</FieldLabel><Input id="delete-password" name="password" type="password" required /></Field>{totpEnabled ? <Field><FieldLabel htmlFor="delete-totp">{ui.profile.authCode}</FieldLabel><Input id="delete-totp" name="totp_code" inputMode="numeric" required /></Field> : null}{mutation.error ? <FieldError>{mutation.error.message}</FieldError> : null}</FieldGroup></CardContent><CardFooter><Button variant="destructive">{ui.profile.deleteTitle}</Button></CardFooter></form></Card>
    <AlertDialog open={deleteBody !== null} onOpenChange={(open) => { if (!open) setDeleteBody(null) }}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>{ui.profile.deleteConfirmTitle}</AlertDialogTitle><AlertDialogDescription>{ui.profile.deleteConfirmBody}</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel>{ui.profile.cancel}</AlertDialogCancel><AlertDialogAction variant="destructive" disabled={mutation.isPending} onClick={(event) => { event.preventDefault(); if (deleteBody) mutation.mutate({ method: 'DELETE', path: '/api/private/me', body: deleteBody }) }}>{ui.profile.deleteForever}</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog>
  </div>
}
