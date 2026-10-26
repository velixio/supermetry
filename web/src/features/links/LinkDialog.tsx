import { useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSet } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { en } from '@/messages/en'

import type { LinkInput, ShortLink } from './types'

const t = en.linksUi

type FormState = {
  name: string
  slug: string
  destination: string
  utm_source: string
  utm_medium: string
  utm_campaign: string
  utm_term: string
  utm_content: string
  enabled: boolean
  expires: string
  clickLimit: string
}

const blank: FormState = {
  name: '', slug: '', destination: '', utm_source: '', utm_medium: '', utm_campaign: '', utm_term: '', utm_content: '',
  enabled: true, expires: '', clickLimit: '',
}

function pad(value: number) {
  return String(value).padStart(2, '0')
}

/** Formats an ISO timestamp for a datetime-local input in the viewer's zone. */
export function toLocalInput(iso: string | null): string {
  if (!iso) return ''
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function fromLink(link: ShortLink | null): FormState {
  if (!link) return blank
  return {
    name: link.name, slug: link.slug, destination: link.destination,
    utm_source: link.utm_source, utm_medium: link.utm_medium, utm_campaign: link.utm_campaign,
    utm_term: link.utm_term, utm_content: link.utm_content,
    enabled: link.enabled, expires: toLocalInput(link.expires_at), clickLimit: link.click_limit ? String(link.click_limit) : '',
  }
}

export function buildInput(form: FormState, existing: ShortLink | null): Partial<LinkInput> {
  const expires = form.expires ? new Date(form.expires).toISOString() : null
  const clickLimit = form.clickLimit.trim() === '' ? null : Number(form.clickLimit)
  const input: Partial<LinkInput> = {
    name: form.name.trim(),
    destination: form.destination.trim(),
    utm_source: form.utm_source.trim(),
    utm_medium: form.utm_medium.trim(),
    utm_campaign: form.utm_campaign.trim(),
    utm_term: form.utm_term.trim(),
    utm_content: form.utm_content.trim(),
    enabled: form.enabled,
    expires_at: expires,
    click_limit: clickLimit,
  }
  const slug = form.slug.trim()
  if (!existing && slug) input.slug = slug
  if (existing && slug && slug !== existing.slug) input.slug = slug
  return input
}

type Props = {
  link: ShortLink | null | undefined
  error: Error | null
  pending: boolean
  onClose: () => void
  onSave: (input: Partial<LinkInput>) => Promise<void>
}

/** Create and edit dialog. `link` is null for a new link and undefined when closed. */
export function LinkDialog({ link, error, pending, onClose, onSave }: Props) {
  if (link === undefined) return null
  return <LinkForm key={link?.id ?? 'new'} link={link} error={error} pending={pending} onClose={onClose} onSave={onSave} />
}

function LinkForm({ link, error, pending, onClose, onSave }: Omit<Props, 'link'> & { link: ShortLink | null }) {
  const [form, setForm] = useState<FormState>(() => fromLink(link))
  const set = (key: keyof FormState) => (event: { target: { value: string } }) =>
    setForm((value) => ({ ...value, [key]: event.target.value }))

  function submit(event: FormEvent) {
    event.preventDefault()
    void onSave(buildInput(form, link)).catch(() => undefined)
  }

  const utmFields = [
    ['utm_source', t.fields.utmSource], ['utm_medium', t.fields.utmMedium], ['utm_campaign', t.fields.utmCampaign],
    ['utm_term', t.fields.utmTerm], ['utm_content', t.fields.utmContent],
  ] as const

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-lg">
        <form className="flex flex-col gap-5" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{link ? t.editTitle : t.createTitle}</DialogTitle>
            <DialogDescription>{t.dialogDescription}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="link-name">{t.fields.name}</FieldLabel>
              <Input id="link-name" className="min-h-11" maxLength={120} required value={form.name} onChange={set('name')} />
            </Field>
            <Field>
              <FieldLabel htmlFor="link-destination">{t.fields.destination}</FieldLabel>
              <Input
                id="link-destination" className="min-h-11" inputMode="url" maxLength={2048} placeholder="https://supermetry.com/landing"
                required type="url" value={form.destination} onChange={set('destination')}
              />
              <FieldDescription>{t.fields.destinationHint}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="link-slug">{t.fields.slug}</FieldLabel>
              <Input
                id="link-slug" autoCapitalize="none" autoComplete="off" className="min-h-11 font-mono" maxLength={64} pattern="[A-Za-z0-9][A-Za-z0-9_\-]{1,63}"
                spellCheck={false} value={form.slug} onChange={set('slug')}
              />
              <FieldDescription>{t.fields.slugHint}</FieldDescription>
            </Field>
            <FieldSet>
              <FieldLegend>{t.fields.utm}</FieldLegend>
              <FieldDescription>{t.fields.utmHint}</FieldDescription>
              <div className="grid gap-3 sm:grid-cols-2">
                {utmFields.map(([key, label]) => (
                  <Field key={key}>
                    <FieldLabel htmlFor={`link-${key}`}>{label}</FieldLabel>
                    <Input id={`link-${key}`} className="min-h-11" maxLength={120} value={form[key]} onChange={set(key)} />
                  </Field>
                ))}
              </div>
            </FieldSet>
            <div className="grid gap-3 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="link-expires">{t.fields.expires}</FieldLabel>
                <Input id="link-expires" className="min-h-11" type="datetime-local" value={form.expires} onChange={set('expires')} />
                <FieldDescription>{t.fields.expiresHint}</FieldDescription>
              </Field>
              <Field>
                <FieldLabel htmlFor="link-limit">{t.fields.clickLimit}</FieldLabel>
                <Input
                  id="link-limit" className="min-h-11" inputMode="numeric" max={1000000000} min={1} step={1} type="number"
                  value={form.clickLimit} onChange={set('clickLimit')}
                />
                <FieldDescription>{t.fields.clickLimitHint}</FieldDescription>
              </Field>
            </div>
            <Field orientation="horizontal">
              <input
                id="link-enabled" checked={form.enabled} className="size-5" type="checkbox"
                onChange={(event) => setForm((value) => ({ ...value, enabled: event.target.checked }))}
              />
              <FieldLabel htmlFor="link-enabled">{t.fields.enabled}</FieldLabel>
            </Field>
          </FieldGroup>
          <FieldError>{error?.message}</FieldError>
          <DialogFooter>
            <Button className="min-h-11" type="button" variant="outline" onClick={onClose}>{t.cancel}</Button>
            <Button className="min-h-11" type="submit" disabled={pending}>{pending ? t.saving : t.save}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
