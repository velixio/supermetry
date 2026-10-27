import { Trash2Icon } from 'lucide-react'
import { useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { en } from '@/messages/en'
import { ErrorLine, formatDate } from '@/features/admin/AdminCommon'
import { useAddStarsAnnotation, useDeleteStarsAnnotation, type StarsAnnotation } from './githubStarsApi'
import { useStarsOrg } from './starsOrg'

/** A value for <input type="datetime-local">: the browser's wall clock, minute precision. */
function localInputValue(date: Date) {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

export function StarsAnnotations({ repoId, annotations }: { repoId: number; annotations: StarsAnnotation[] }) {
  const { canManage } = useStarsOrg()
  const add = useAddStarsAnnotation(repoId)
  const remove = useDeleteStarsAnnotation(repoId)
  const [label, setLabel] = useState('')
  const [at, setAt] = useState(() => localInputValue(new Date()))
  const submit = (event: FormEvent) => {
    event.preventDefault()
    const when = new Date(at)
    if (!label.trim() || Number.isNaN(when.getTime())) return
    add.mutate({ at: when.toISOString(), label: label.trim() }, { onSuccess: () => setLabel('') })
  }
  return (
    <section aria-labelledby="stars-annotations" className="flex min-w-0 flex-col gap-3">
      <h2 id="stars-annotations" className="text-base font-semibold">{en.githubStars.annotations}</h2>
      {canManage ? <form className="flex flex-wrap items-end gap-2" onSubmit={submit}>
        <label className="flex min-w-0 flex-1 basis-48 flex-col gap-1 text-sm font-medium">
          {en.githubStars.annotationLabel}
          <Input className="min-h-11" value={label} maxLength={200} required onChange={(event) => setLabel(event.target.value)} />
        </label>
        <label className="flex min-w-0 flex-1 basis-48 flex-col gap-1 text-sm font-medium">
          {en.githubStars.annotationAt}
          <Input className="min-h-11" type="datetime-local" value={at} required onChange={(event) => setAt(event.target.value)} />
        </label>
        <Button type="submit" className="min-h-11" disabled={add.isPending || !label.trim()}>
          {add.isPending ? en.githubStars.annotationAdding : en.githubStars.annotationAdd}
        </Button>
      </form> : null}
      <ErrorLine error={add.error ?? remove.error} />
      {annotations.length === 0 ? <p className="text-sm text-muted-foreground">{en.githubStars.annotationsEmpty}</p> : (
        <ul className="flex flex-col divide-y rounded-lg border">
          {annotations.map((annotation) => (
            <li key={annotation.id} className="flex items-center justify-between gap-3 px-3 py-1">
              <span className="min-w-0 break-words text-sm"><strong className="font-medium">{annotation.label}</strong> <span className="text-muted-foreground">{formatDate(annotation.at)}</span></span>
              {canManage ? <Button type="button" variant="ghost" size="icon" className="size-11 shrink-0" disabled={remove.isPending}
                aria-label={en.githubStars.annotationDelete(annotation.label)} onClick={() => remove.mutate(annotation.id)}>
                <Trash2Icon aria-hidden="true" />
              </Button> : null}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
