import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
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
import { PasswordInput } from '@/features/auth/PasswordInput'
import type { UserPreferences } from '@/lib/me'
import { manifest, pickerLocales, type LocaleCode } from '@/lib/i18n'
import { en } from '@/messages/en'
import { privateRequest } from './privateApi'
import { useSaveLocalePreference } from './useSaveLocalePreference'
import { uiAccount } from '@/messages/uiAccount'
import { uiFlows } from '@/messages/uiFlows'
import { nativeSelectClass } from '@/lib/nativeSelect'

type Method = 'PATCH' | 'POST' | 'DELETE'
type Request = (method: Method, path: string, body: Record<string, unknown>) => Promise<unknown>


const errorText = (error: unknown) => (error instanceof Error ? error.message : en.auth.serverError)

/** One request per form, so each card shows its own pending state, error and confirmation. */
function useFormRequest(request: Request, onSuccess?: (variables: { method: Method; path: string; body: Record<string, unknown> }) => void) {
  const [done, setDone] = useState(false)
  const mutation = useMutation({
    mutationFn: (variables: { method: Method; path: string; body: Record<string, unknown> }) => request(variables.method, variables.path, variables.body),
    onMutate: () => setDone(false),
    onSuccess: (_, variables) => {
      setDone(true)
      onSuccess?.(variables)
    },
  })
  return { ...mutation, done }
}

function Status({ children }: { children: string }) {
  return <p role="status" className="text-sm text-muted-foreground">{children}</p>
}

const fields = (event: FormEvent<HTMLFormElement>, names: string[]) => {
  const data = new FormData(event.currentTarget)
  return Object.fromEntries(names.map((field) => [field, String(data.get(field) ?? '')]))
}

export function AccountProfilePage({ name, email, totpEnabled, preferences, request = (method, path, body) => privateRequest(method, path, { body }), onDeleted }: {
  name: string; email: string; totpEnabled: boolean; preferences?: UserPreferences; request?: Request; onDeleted: () => void
}) {
  const queryClient = useQueryClient()
  const [deleteBody, setDeleteBody] = useState<Record<string, unknown> | null>(null)
  const localeSave = useSaveLocalePreference(request)
  const refreshMe = () => void queryClient.invalidateQueries({ queryKey: ['me'] })
  const nameSave = useFormRequest(request, refreshMe)
  const landingSave = useFormRequest(request, refreshMe)
  const emailChange = useFormRequest(request)
  const passwordChange = useFormRequest(request)
  const accountDelete = useFormRequest(request, () => onDeleted())

  const nameError = nameSave.error ? errorText(nameSave.error) : ''
  const emailError = emailChange.error ? errorText(emailChange.error) : ''
  const passwordError = passwordChange.error ? errorText(passwordChange.error) : ''

  return <PageContainer width="narrow">
    <PageHeader title={uiAccount.profile.title} description={uiAccount.profile.intro} />
    <Card><CardHeader><CardTitle>{uiAccount.profile.displayName}</CardTitle><CardDescription>{uiAccount.profile.displayNameNote}</CardDescription></CardHeader>
      <form onSubmit={(event) => { event.preventDefault(); nameSave.mutate({ method: 'PATCH', path: '/api/private/me', body: fields(event, ['name']) }) }}>
        <CardContent><FieldGroup><Field data-invalid={Boolean(nameError) || undefined}>
          <FieldLabel htmlFor="display-name">{uiAccount.profile.displayName}</FieldLabel>
          <Input id="display-name" name="name" defaultValue={name} autoComplete="name" required aria-invalid={Boolean(nameError) || undefined} aria-describedby={nameError ? 'display-name-error' : undefined} />
          {nameError ? <FieldError id="display-name-error">{nameError}</FieldError> : null}
          {nameSave.done ? <Status>{uiFlows.account.nameSaved}</Status> : null}
        </Field></FieldGroup></CardContent>
        <CardFooter><Button disabled={nameSave.isPending}>{nameSave.isPending ? uiFlows.common.saving : uiAccount.profile.saveName}</Button></CardFooter>
      </form></Card>
    <Card><CardHeader><CardTitle>{uiAccount.profile.landingTitle}</CardTitle><CardDescription>{uiAccount.profile.landingNote}</CardDescription></CardHeader>
      <CardContent><FieldGroup><Field data-invalid={Boolean(landingSave.error) || undefined}>
        <FieldLabel htmlFor="landing-page">{uiAccount.profile.afterSignIn}</FieldLabel>
        <select id="landing-page" className={nativeSelectClass} defaultValue={preferences?.landing_page ?? 'overview'} disabled={landingSave.isPending} aria-describedby="landing-page-hint" onChange={(event) => landingSave.mutate({ method: 'PATCH', path: '/api/private/me', body: { preferences: { landing_page: event.target.value } } })}>
          <option value="overview">{uiAccount.profile.sitesOverview}</option><option value="last_site">{uiAccount.profile.lastSite}</option>
        </select>
        <FieldDescription id="landing-page-hint">{uiAccount.profile.landingHint}</FieldDescription>
        {landingSave.error ? <FieldError>{errorText(landingSave.error)}</FieldError> : null}
        {landingSave.done ? <Status>{uiFlows.account.landingSaved}</Status> : null}
      </Field></FieldGroup></CardContent></Card>
    <Card><CardHeader><CardTitle>{en.language.title}</CardTitle><CardDescription>{en.language.description}</CardDescription></CardHeader>
      <CardContent><FieldGroup><Field><FieldLabel htmlFor="interface-language">{en.language.label}</FieldLabel><select id="interface-language" className={nativeSelectClass} defaultValue={preferences?.locale ?? ''} disabled={localeSave.isPending} onChange={(event) => localeSave.save(event.target.value as LocaleCode | '')}><option value="">{en.language.browserDefault}</option>{pickerLocales.map((code) => <option key={code} value={code}>{manifest[code].nativeName}{manifest[code].reviewed ? '' : ` ${en.language.machineTranslated}`}</option>)}</select><FieldDescription>{en.language.note}</FieldDescription></Field></FieldGroup></CardContent></Card>
    <Card><CardHeader><CardTitle>{uiAccount.profile.emailTitle}</CardTitle><CardDescription>{uiAccount.profile.emailNote(email)}</CardDescription></CardHeader>
      <form onSubmit={(event) => { event.preventDefault(); emailChange.mutate({ method: 'POST', path: '/api/private/me/email', body: fields(event, ['new_email', 'password']) }) }}>
        <CardContent><FieldGroup>
          <Field><FieldLabel htmlFor="new-email">{uiAccount.profile.newEmail}</FieldLabel><Input id="new-email" name="new_email" type="email" autoComplete="email" spellCheck={false} autoCapitalize="none" required /></Field>
          <Field data-invalid={Boolean(emailError) || undefined}>
            <FieldLabel htmlFor="email-password">{uiAccount.profile.emailPassword}</FieldLabel>
            <PasswordInput id="email-password" name="password" autoComplete="current-password" required aria-invalid={Boolean(emailError) || undefined} aria-describedby={emailError ? 'email-change-error' : undefined} />
            {emailError ? <FieldError id="email-change-error">{emailError}</FieldError> : null}
          </Field>
          {emailChange.done ? <Status>{uiAccount.profile.emailSent}</Status> : null}
        </FieldGroup></CardContent>
        <CardFooter><Button disabled={emailChange.isPending}>{emailChange.isPending ? uiFlows.account.sending : uiAccount.profile.sendConfirmation}</Button></CardFooter>
      </form></Card>
    <Card><CardHeader><CardTitle>{uiAccount.profile.passwordTitle}</CardTitle><CardDescription>{uiAccount.profile.passwordNote}</CardDescription></CardHeader>
      <form onSubmit={(event) => { event.preventDefault(); const form = event.currentTarget; passwordChange.mutate({ method: 'POST', path: '/api/private/me/password', body: fields(event, ['current_password', 'new_password']) }, { onSuccess: () => form.reset() }) }}>
        <CardContent><FieldGroup>
          <Field data-invalid={Boolean(passwordError) || undefined}>
            <FieldLabel htmlFor="current-password">{uiAccount.profile.currentPassword}</FieldLabel>
            <PasswordInput id="current-password" name="current_password" autoComplete="current-password" required aria-invalid={Boolean(passwordError) || undefined} aria-describedby={passwordError ? 'password-change-error' : undefined} />
            {passwordError ? <FieldError id="password-change-error">{passwordError}</FieldError> : null}
          </Field>
          <Field>
            <FieldLabel htmlFor="new-password">{uiAccount.profile.newPassword}</FieldLabel>
            <PasswordInput id="new-password" name="new_password" autoComplete="new-password" minLength={6} required aria-describedby="new-password-hint" />
            <FieldDescription id="new-password-hint">{en.auth.passwordPolicy}</FieldDescription>
          </Field>
          {passwordChange.done ? <Status>{uiFlows.account.passwordChanged}</Status> : null}
        </FieldGroup></CardContent>
        <CardFooter><Button disabled={passwordChange.isPending}>{passwordChange.isPending ? uiFlows.account.changing : uiAccount.profile.changePassword}</Button></CardFooter>
      </form></Card>
    <Card><CardHeader><CardTitle>{uiAccount.profile.deleteTitle}</CardTitle><CardDescription>{uiAccount.profile.deleteNote}</CardDescription></CardHeader>
      <form onSubmit={(event) => { event.preventDefault(); const data = new FormData(event.currentTarget); const body: Record<string, unknown> = { password: String(data.get('password') ?? '') }; if (totpEnabled) body.totp_code = String(data.get('totp_code') ?? ''); accountDelete.reset(); setDeleteBody(body) }}>
        <CardContent><FieldGroup>
          <Field><FieldLabel htmlFor="delete-password">{uiAccount.profile.deletePassword}</FieldLabel><PasswordInput id="delete-password" name="password" autoComplete="current-password" required /></Field>
          {totpEnabled ? <Field><FieldLabel htmlFor="delete-totp">{uiAccount.profile.authCode}</FieldLabel><Input id="delete-totp" name="totp_code" inputMode="numeric" autoComplete="one-time-code" required /></Field> : null}
        </FieldGroup></CardContent>
        <CardFooter><Button variant="destructive">{uiAccount.profile.deleteTitle}</Button></CardFooter>
      </form></Card>
    <AlertDialog open={deleteBody !== null} onOpenChange={(open) => { if (!open && !accountDelete.isPending) setDeleteBody(null) }}>
      <AlertDialogContent>
        <AlertDialogHeader><AlertDialogTitle>{uiAccount.profile.deleteConfirmTitle}</AlertDialogTitle><AlertDialogDescription>{uiAccount.profile.deleteConfirmBody}</AlertDialogDescription></AlertDialogHeader>
        {accountDelete.error ? <FieldError>{errorText(accountDelete.error)}</FieldError> : null}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={accountDelete.isPending}>{uiAccount.profile.cancel}</AlertDialogCancel>
          <AlertDialogAction variant="destructive" disabled={accountDelete.isPending} onClick={(event) => { event.preventDefault(); if (deleteBody) accountDelete.mutate({ method: 'DELETE', path: '/api/private/me', body: deleteBody }) }}>{accountDelete.isPending ? uiFlows.account.deleting : uiAccount.profile.deleteForever}</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </PageContainer>
}
