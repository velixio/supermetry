import { useState, type ReactNode } from 'react'
import { PlusIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'

import { AnnotationEditor } from './AnnotationEditor'
import {
  useAnnotations,
  useCreateAnnotation,
  useDeleteAnnotation,
  useUpdateAnnotation,
} from './api'
import type { Annotation, AnnotationInput } from './types'

type AnnotationActions = {
  /** The "Add annotation" button, for the chart card's header; null without edit rights. */
  addButton: ReactNode
  annotations: Annotation[]
  onEdit: (annotation: Annotation) => void
  onDelete: (annotation: Annotation) => void
}

type AnnotationManagerProps = {
  siteId: number
  dateRange: { from: string; to: string }
  canEdit: boolean
  children: (actions: AnnotationActions) => ReactNode
}

export function AnnotationManager({
  siteId,
  dateRange,
  canEdit,
  children,
}: AnnotationManagerProps) {
  const annotations = useAnnotations(siteId, dateRange.from, dateRange.to)
  const create = useCreateAnnotation(siteId)
  const update = useUpdateAnnotation(siteId)
  const remove = useDeleteAnnotation(siteId)
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Annotation>()

  const startAdd = () => {
    setEditing(undefined)
    setOpen(true)
  }
  const startEdit = (annotation: Annotation) => {
    if (!canEdit) return
    setEditing(annotation)
    setOpen(true)
  }
  const save = (input: AnnotationInput) => {
    const options = { onSuccess: () => setOpen(false) }
    if (editing) {
      update.mutate({ id: editing.id, input }, options)
    } else {
      create.mutate(input, options)
    }
  }
  const deleteAnnotation = (annotation: Annotation) => {
    if (!canEdit) return
    remove.mutate(annotation.id)
  }

  const addButton = canEdit ? (
    <Button size="sm" variant="outline" onClick={startAdd}>
      <PlusIcon data-icon="inline-start" />
      Add annotation
    </Button>
  ) : null

  return (
    <>
      {children({
        addButton,
        annotations: annotations.data ?? [],
        onEdit: startEdit,
        onDelete: deleteAnnotation,
      })}
      {canEdit ? (
        <AnnotationEditor
          annotation={editing}
          defaultDay={dateRange.to}
          open={open}
          pending={create.isPending || update.isPending}
          error={create.error ?? update.error}
          onOpenChange={setOpen}
          onSave={save}
        />
      ) : null}
    </>
  )
}
