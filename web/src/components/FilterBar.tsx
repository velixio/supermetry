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
import { CohortMenuItems, isCohortFilter, useCohortFilterName } from '@/features/cohorts/FilterBarCohorts'
import { useDeleteSegment, useSaveSegment, useSegments } from '@/features/segments/api'
import type { Segment } from '@/features/segments/types'
import { useCapability } from '@/lib/authorization'
import { serializeFilters, type Filter } from '@/lib/search'

import { Flag } from './Flag'
import { AddFilterPicker } from './AddFilterPicker'
import { ui } from '@/messages/ui'

type FilterBarProps = {
  siteId: number
  orgId?: number
  canEdit: boolean
  filters: Filter[]
  dropped: string[]
  invalidNotice: string
  filterLabel: (filter: Filter) => string
  removeLabel: (filter: Filter) => string
  onApply: (filters: Filter[]) => void
  onRemove: (index: number) => void
  dateRange?: { from: string; to: string }
  onAdd?: (filter: Filter) => void
}

function sameFilters(left: Filter[], right: Filter[]): boolean {
  const canonical = (filters: Filter[]) =>
    serializeFilters(
      filters.map((filter) => ({
        ...filter,
        values: filter.dimension === 'region' || filter.dimension === 'city'
          ? filter.values
          : [...filter.values].sort(),
      })),
    ).sort()
  return JSON.stringify(canonical(left)) === JSON.stringify(canonical(right))
}

export function FilterBar({
  siteId,
  orgId,
  canEdit,
  filters,
  dropped,
  invalidNotice,
  filterLabel,
  removeLabel,
  onApply,
  onRemove,
  dateRange,
  onAdd,
}: FilterBarProps) {
  const segments = useSegments(siteId)
  const canReadProfiles = useCapability('profiles.read')
  const cohortName = useCohortFilterName(siteId, filters)
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
      setError(ui.segments.nameLength)
      return
    }
    try {
      await saveSegment.mutateAsync({
        id: editing?.id,
        input: { name: trimmedName, filters: editing?.filters ?? filters },
      })
      setEditorOpen(false)
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : ui.segments.saveFailed)
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
          <DropdownMenuLabel>{ui.segments.saved}</DropdownMenuLabel>
          <DropdownMenuGroup>
            {segments.data?.map((segment) => (
              <DropdownMenuItem key={segment.id} onSelect={() => onApply(segment.filters)}>
                {segment.name}
              </DropdownMenuItem>
            ))}
            {segments.isPending ? <DropdownMenuItem disabled>{ui.segments.loading}</DropdownMenuItem> : null}
            {segments.data?.length === 0 ? <DropdownMenuItem disabled>{ui.segments.none}</DropdownMenuItem> : null}
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
          {canReadProfiles ? <CohortMenuItems filters={filters} siteId={siteId} onApply={onApply} /> : null}
          <DropdownMenuSeparator />
          <DropdownMenuGroup>
            <DropdownMenuItem disabled={!canEdit || filters.length === 0} onSelect={() => openEditor()}>
              Save current filters as segment…
            </DropdownMenuItem>
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      {dateRange && onAdd && orgId !== undefined ? <AddFilterPicker dateRange={dateRange} orgId={orgId} siteId={siteId} onAdd={onAdd} /> : null}
      {filters.map((filter, index) => (
        <Button
          key={`${filter.dimension}:${filter.operator}:${filter.values.join(',')}:${index}`}
          aria-label={isCohortFilter(filter) ? `Remove cohort filter ${cohortName(filter)}` : removeLabel(filter)}
          type="button"
          variant="secondary"
          onClick={() => onRemove(index)}
        >
          {filter.dimension === 'country' ? (
            <span aria-hidden="true" className="flex items-center gap-1">
              {filter.values.map((code) => <Flag key={code} code={code} />)}
            </span>
          ) : null}
          {isCohortFilter(filter) ? `Cohort: ${cohortName(filter)}` : filterLabel(filter)}
          <span aria-hidden="true" className="text-muted-foreground">×</span>
        </Button>
      ))}
      <Dialog open={editorOpen} onOpenChange={setEditorOpen}>
        <DialogContent>
          <form className="contents" onSubmit={submit}>
            <DialogHeader>
              <DialogTitle>{editing ? ui.segments.rename : ui.segments.save}</DialogTitle>
              <DialogDescription>
                {editing ? ui.segments.renameNote : ui.segments.saveNote}
              </DialogDescription>
            </DialogHeader>
            <FieldGroup>
              <Field data-invalid={Boolean(error || saveSegment.error)}>
                <FieldLabel htmlFor="segment-name">{ui.segments.name}</FieldLabel>
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
              <DialogClose asChild><Button type="button" variant="outline">{ui.segments.cancel}</Button></DialogClose>
              <Button disabled={saveSegment.isPending} type="submit">
                {saveSegment.isPending ? ui.segments.saving : editing ? ui.segments.rename : ui.segments.save}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <AlertDialog open={Boolean(deleting)} onOpenChange={(open) => !open && setDeleting(undefined)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{ui.segments.deleteTitle}</AlertDialogTitle>
            <AlertDialogDescription>{ui.segments.deleteBody(deleting?.name ?? '')}</AlertDialogDescription>
          </AlertDialogHeader>
          {deleteSegment.isError ? <p role="alert">{deleteSegment.error.message}</p> : null}
          <AlertDialogFooter>
            <AlertDialogCancel>{ui.segments.cancel}</AlertDialogCancel>
            <AlertDialogAction
              disabled={deleteSegment.isPending}
              variant="destructive"
              onClick={(event) => {
                event.preventDefault()
                void confirmDelete()
              }}
            >
              {deleteSegment.isPending ? ui.segments.deleting : ui.segments.delete}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
