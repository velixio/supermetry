import { useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { en } from '@/messages/en'

import type { TrackingPixel, TrackingPixelInput } from './types'

const t = en.linksUi
const tagPattern = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$/

type Props = {
  pixel: TrackingPixel | null | undefined
  error: Error | null
  pending: boolean
  onClose: () => void
  onSave: (input: TrackingPixelInput) => Promise<void>
}

/** Create and rename dialog. `pixel` is null for a new pixel and undefined when closed. */
export function PixelDialog({ pixel, error, pending, onClose, onSave }: Props) {
  if (pixel === undefined) return null
  return <PixelForm key={pixel?.id ?? 'new'} pixel={pixel} error={error} pending={pending} onClose={onClose} onSave={onSave} />
}

function PixelForm({ pixel, error, pending, onClose, onSave }: Omit<Props, 'pixel'> & { pixel: TrackingPixel | null }) {
  const [name, setName] = useState(pixel?.name ?? '')
  const [tag, setTag] = useState('')
  const trimmedTag = tag.trim()
  const tagValid = trimmedTag === '' || tagPattern.test(trimmedTag)

  function submit(event: FormEvent) {
    event.preventDefault()
    if (!tagValid) return
    void onSave({ name: name.trim(), ...(pixel || !trimmedTag ? {} : { tag: trimmedTag }) }).catch(() => undefined)
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent className="sm:max-w-md">
        <form className="flex flex-col gap-5" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{pixel ? t.pixelEditTitle : t.pixelCreateTitle}</DialogTitle>
            <DialogDescription>{pixel ? t.pixelRenameDescription : t.pixelDialogDescription}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="pixel-name">{t.pixelName}</FieldLabel>
              <Input
                id="pixel-name"
                className="min-h-11"
                maxLength={120}
                required
                value={name}
                onChange={(event) => setName(event.target.value)}
              />
            </Field>
            {!pixel ? (
              <Field data-invalid={!tagValid}>
                <FieldLabel htmlFor="pixel-tag">{t.pixelTag}</FieldLabel>
                <Input
                  id="pixel-tag"
                  autoCapitalize="none"
                  autoComplete="off"
                  className="min-h-11"
                  maxLength={64}
                  aria-invalid={!tagValid}
                  spellCheck={false}
                  value={tag}
                  onChange={(event) => setTag(event.target.value)}
                />
                <FieldDescription>{t.pixelTagHint}</FieldDescription>
                {!tagValid ? <FieldError>{t.pixelTagInvalid}</FieldError> : null}
              </Field>
            ) : null}
          </FieldGroup>
          <FieldError>{error?.message}</FieldError>
          <DialogFooter>
            <Button className="min-h-11" type="button" variant="outline" onClick={onClose}>{t.cancel}</Button>
            <Button className="min-h-11" disabled={pending} type="submit">
              {pending ? t.pixelSaving : pixel ? t.pixelSave : t.pixelCreate}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
