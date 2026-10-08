import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Field, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

import type { Annotation, AnnotationInput } from './types'

type AnnotationEditorProps = {
  annotation?: Annotation
  defaultDay: string
  open: boolean
  pending: boolean
  error?: Error | null
  onOpenChange: (open: boolean) => void
  onSave: (input: AnnotationInput) => void
}

export function AnnotationEditor({
  annotation,
  defaultDay,
  open,
  pending,
  error,
  onOpenChange,
  onSave,
}: AnnotationEditorProps) {
  const [day, setDay] = useState(annotation?.day ?? defaultDay)
  const [text, setText] = useState(annotation?.text ?? '')
  const [validation, setValidation] = useState('')

  useEffect(() => {
    if (!open) return
    setDay(annotation?.day ?? defaultDay)
    setText(annotation?.text ?? '')
    setValidation('')
  }, [annotation, defaultDay, open])

  const submit = (event: FormEvent) => {
    event.preventDefault()
    const trimmed = text.trim()
    if (trimmed.length < 1 || trimmed.length > 200) {
      setValidation('Annotation text must be between 1 and 200 characters.')
      return
    }
    setValidation('')
    onSave({ day, text: trimmed })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{annotation ? 'Edit annotation' : 'Add annotation'}</DialogTitle>
          <DialogDescription>
            Add context to the dashboard chart for this site-local day.
          </DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-4" onSubmit={submit}>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="annotation-day">Day</FieldLabel>
              <Input
                id="annotation-day"
                type="date"
                required
                value={day}
                onChange={(event) => setDay(event.target.value)}
              />
            </Field>
            <Field data-invalid={validation !== ''}>
              <FieldLabel htmlFor="annotation-text">Annotation</FieldLabel>
              <Textarea
                id="annotation-text"
                aria-invalid={validation !== ''}
                maxLength={200}
                value={text}
                onChange={(event) => setText(event.target.value)}
              />
              {validation ? <FieldError>{validation}</FieldError> : null}
            </Field>
          </FieldGroup>
          {error ? <p role="alert" className="text-sm text-destructive">{error.message}</p> : null}
          <DialogFooter>
            <Button type="submit" disabled={pending}>Save annotation</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
