import { useMemo, useState, type FormEvent } from 'react'
import { ChevronDownIcon, PencilIcon, Trash2Icon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Field, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { useDeleteSegment, useSaveSegment, useSegments } from '@/features/segments/api'
import type { Segment } from '@/features/segments/types'
import { serializeFilters, type Filter } from '@/lib/search'

type FilterBarProps = {
  siteId: number
  canEdit: boolean
  filters: Filter[]
  dropped: string[]
  invalidNotice: string
  filterLabel: (filter: Filter) => string
  removeLabel: (filter: Filter) => string
  onApply: (filters: Filter[]) => void
  onRemove: (index: number) => void
}

function sameFilters(left: Filter[], right: Filter[]): boolean {
  const canonical = (filters: Filter[]) =>
    serializeFilters(
      filters.map((filter) => ({ ...filter, values: [...filter.values].sort() })),
    ).sort()
  return JSON.stringify(canonical(left)) === JSON.stringify(canonical(right))
}

export function FilterBar({
  siteId,
  canEdit,
  filters,
  dropped,
  invalidNotice,
  filterLabel,
  removeLabel,
  onApply,
  onRemove,
}: FilterBarProps) {
  const segments = useSegments(siteId)
  const saveSegment = useSaveSegment(siteId)
  const deleteSegment = useDeleteSegment(siteId)
  const [editorOpen, setEditorOpen] = useState(false)
  const [editing, setEditing] = useState<Segment>()
  const [deleting, setDeleting] = useState<Segment>()
  const [name, setName] = useState('')
  const [error, setError] = useState('')
  const activeSegment = useMemo(
    () => segments.data?.find((segment) => sameFilters(filters, segment.filters)),
    [filters, segments.data],
  )

  const openEditor = (segment?: Segment) => {
    setEditing(segment)
    setName(segment?.name ?? '')
    setError('')
    saveSegment.reset()
    setEditorOpen(true)
  }

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const trimmedName = name.trim()
    const nameLength = Array.from(trimmedName).length
    if (nameLength < 1 || nameLength > 80) {
      setError('Name must be between 1 and 80 characters.')
      return
    }
    try {
      await saveSegment.mutateAsync({
        id: editing?.id,
        input: { name: trimmedName, filters: editing?.filters ?? filters },
      })
      setEditorOpen(false)
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'Could not save the segment.')
    }
  }

  const confirmDelete = async () => {
    if (!deleting) return
    try {
      await deleteSegment.mutateAsync(deleting.id)
      setDeleting(undefined)
    } catch {
      // The mutation error remains visible in the confirmation dialog.
    }
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
      {dropped.length > 0 ? (
        <p
          aria-live="polite"
          className="fixed right-4 bottom-4 rounded-md border bg-popover px-4 py-3 text-sm text-popover-foreground shadow-md"
          role="status"
        >
          {invalidNotice}
        </p>
      ) : null}
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            aria-label={activeSegment ? `Segments: ${activeSegment.name}` : 'Segments'}
            type="button"
            variant="outline"
          >
            {activeSegment?.name ?? 'Segments'}
            <ChevronDownIcon data-icon="inline-end" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="min-w-56">
          <DropdownMenuLabel>Saved segments</DropdownMenuLabel>
          <DropdownMenuGroup>
            {segments.data?.map((segment) => (
              <DropdownMenuItem key={segment.id} onSelect={() => onApply(segment.filters)}>
                {segment.name}
              </DropdownMenuItem>
            ))}
            {segments.isPending ? <DropdownMenuItem disabled>Loading…</DropdownMenuItem> : null}
            {segments.data?.length === 0 ? <DropdownMenuItem disabled>No saved segments</DropdownMenuItem> : null}
            {segments.isError ? (
              <DropdownMenuItem disabled>{segments.error.message}</DropdownMenuItem>
            ) : null}
          </DropdownMenuGroup>
          {canEdit && segments.data && segments.data.length > 0 ? (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuGroup>
                {segments.data.map((segment) => (
                  <DropdownMenuItem key={`rename-${segment.id}`} onSelect={() => openEditor(segment)}>
                    <PencilIcon data-icon="inline-start" />
                    Rename {segment.name}
                  </DropdownMenuItem>
                ))}
                {segments.data.map((segment) => (
                  <DropdownMenuItem
                    key={`delete-${segment.id}`}
                    variant="destructive"
                    onSelect={() => setDeleting(segment)}
                  >
                    <Trash2Icon data-icon="inline-start" />
                    Delete {segment.name}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuGroup>
            </>
          ) : null}
          <DropdownMenuSeparator />
          <DropdownMenuGroup>
            <DropdownMenuItem disabled={!canEdit || filters.length === 0} onSelect={() => openEditor()}>
              Save current filters as segment…
            </DropdownMenuItem>
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      {filters.map((filter, index) => (
        <Button
          key={`${filter.dimension}:${filter.operator}:${filter.values.join(',')}:${index}`}
          aria-label={removeLabel(filter)}
          type="button"
          variant="secondary"
          onClick={() => onRemove(index)}
        >
          {filterLabel(filter)}
          <span aria-hidden="true" className="text-muted-foreground">×</span>
        </Button>
      ))}
      <Dialog open={editorOpen} onOpenChange={setEditorOpen}>
        <DialogContent>
          <form className="contents" onSubmit={submit}>
            <DialogHeader>
              <DialogTitle>{editing ? 'Rename segment' : 'Save segment'}</DialogTitle>
              <DialogDescription>
                {editing ? 'Choose a new name for this saved segment.' : 'Save the current dashboard filters for this site.'}
              </DialogDescription>
            </DialogHeader>
            <FieldGroup>
              <Field data-invalid={Boolean(error || saveSegment.error)}>
                <FieldLabel htmlFor="segment-name">Segment name</FieldLabel>
                <Input
                  id="segment-name"
                  aria-invalid={Boolean(error || saveSegment.error)}
                  autoFocus
                  minLength={1}
                  required
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                />
                <FieldError>{error || saveSegment.error?.message}</FieldError>
              </Field>
            </FieldGroup>
            <DialogFooter>
              <DialogClose asChild><Button type="button" variant="outline">Cancel</Button></DialogClose>
              <Button disabled={saveSegment.isPending} type="submit">
                {saveSegment.isPending ? 'Saving…' : editing ? 'Rename segment' : 'Save segment'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <AlertDialog open={Boolean(deleting)} onOpenChange={(open) => !open && setDeleting(undefined)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete segment?</AlertDialogTitle>
            <AlertDialogDescription>Delete “{deleting?.name}”? This cannot be undone.</AlertDialogDescription>
          </AlertDialogHeader>
          {deleteSegment.isError ? <p role="alert">{deleteSegment.error.message}</p> : null}
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={deleteSegment.isPending}
              variant="destructive"
              onClick={(event) => {
                event.preventDefault()
                void confirmDelete()
              }}
            >
              {deleteSegment.isPending ? 'Deleting…' : 'Delete segment'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
