import { useBlocker } from '@tanstack/react-router'
import { useEffect, useLayoutEffect, useMemo, useReducer, useRef, useState } from 'react'
import { PlusIcon } from 'lucide-react'

import { ErrorState } from '@/components/ErrorState'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription,
  AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Field, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import type { Compare } from '@/lib/search'
import type { StatsQuery } from '@/lib/stats'
import { dashEdit } from '@/messages/dashEdit'
import { en } from '@/messages/en'

import { useDashboards, type Dashboard } from '../api'
import { widgetQuery } from '../layout'
import { DashboardSettings } from './DashboardSettings'
import { conflictKind, problemMessages, useCreateDashboard, useDeleteDashboard, useDuplicateDashboard, useSaveDashboard, useSetDefaultDashboard, type Problem } from './editorApi'
import {
  isDirty, limits, makeWidget, normalizeLayout, reduceDraft, templateLayout, validateDraft,
  type Draft, type TemplateKey,
} from './model'
import { issueText, widgetLabel } from './text'
import { WidgetDialog } from './WidgetDialog'
import { WidgetSlot, type SlotAction } from './WidgetSlot'
import './editor.css'

const t = dashEdit

export type EditorMode = { kind: 'edit'; id: number } | { kind: 'new'; template: TemplateKey }

export type DashboardEditorProps = {
  mode: EditorMode
  siteId: number
  orgId?: number
  canWrite: boolean
  anonymous: boolean
  base: StatsQuery
  timezone: string
  /** A new or duplicated dashboard exists now; the route moves to it and keeps editing. */
  onOpen: (dashboard: Dashboard) => void
  /** Leave edit mode, staying on the dashboard. */
  onExit: () => void
  /** The dashboard being edited was deleted. */
  onDeleted: () => void
}

function uniqueName(existing: string[], wanted: string): string {
  const taken = new Set(existing.map((name) => name.toLowerCase()))
  if (!taken.has(wanted.toLowerCase())) return wanted
  for (let n = 2; ; n += 1) {
    const candidate = `${wanted} ${n}`
    if (!taken.has(candidate.toLowerCase())) return candidate
  }
}

/** Asks before in-app navigation or a reload throws away unsaved edits. */
function UnsavedGuard({ active, bypass }: { active: boolean; bypass: { current: boolean } }) {
  const activeRef = useRef(active)
  activeRef.current = active
  const blocker = useBlocker({
    shouldBlockFn: () => activeRef.current && !bypass.current,
    enableBeforeUnload: () => activeRef.current && !bypass.current,
    withResolver: true,
  })
  return (
    <AlertDialog open={blocker.status === 'blocked'}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t.guardTitle}</AlertDialogTitle>
          <AlertDialogDescription>{t.guardBody}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel className="min-h-11 sm:min-h-9" onClick={() => blocker.reset?.()}>{t.guardStay}</AlertDialogCancel>
          <AlertDialogAction className="min-h-11 sm:min-h-9" variant="destructive" onClick={() => blocker.proceed?.()}>{t.guardLeave}</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

type Start = { id?: number; version?: number; draft: Draft }
type DialogState = { mode: 'add' | 'edit'; widget: ReturnType<typeof makeWidget> } | null

export function DashboardEditor(props: DashboardEditorProps) {
  const dashboards = useDashboards(props.siteId)
  if (!props.canWrite) return <p className="rounded-xl border bg-card p-4 text-sm" role="status">{t.readOnly}</p>
  if (dashboards.isError) return <ErrorState detail={dashboards.error?.detail ?? t.loadError} onRetry={() => void dashboards.refetch()} />
  if (dashboards.isPending) {
    return (
      <div aria-busy="true" aria-label={t.loading} className="flex flex-col gap-3" role="status">
        <Skeleton className="h-16 w-full rounded-xl" />
        <Skeleton className="h-64 w-full rounded-xl" />
      </div>
    )
  }
  let start: Start
  if (props.mode.kind === 'edit') {
    const id = props.mode.id
    const found = dashboards.data.find((item) => item.id === id && !item.builtin)
    if (!found) {
      return (
        <section className="flex flex-col items-start gap-3 rounded-xl border bg-card p-5" role="status">
          <p className="text-sm">{t.gone}</p>
          <Button className="min-h-11 sm:min-h-9" type="button" variant="outline" onClick={props.onExit}>{t.done}</Button>
        </section>
      )
    }
    start = { id: found.id, version: found.version, draft: { name: found.name, layout: normalizeLayout(found.layout) } }
  } else {
    const names = dashboards.data.filter((item) => !item.builtin).map((item) => item.name)
    start = { draft: { name: uniqueName(names, t.newDashboard), layout: templateLayout(props.mode.template) } }
  }
  return <EditorBody key={props.mode.kind === 'edit' ? `edit-${props.mode.id}` : 'new'} {...props} dashboards={dashboards.data} refetch={() => dashboards.refetch()} start={start} />
}

type BodyProps = DashboardEditorProps & {
  dashboards: Dashboard[]
  refetch: () => Promise<{ data?: Dashboard[] }>
  start: Start
}

function EditorBody({ siteId, orgId, anonymous, base, timezone, dashboards, refetch, start, onOpen, onExit, onDeleted }: BodyProps) {
  const [draft, dispatch] = useReducer(reduceDraft, start.draft)
  const [baseline, setBaseline] = useState<Draft>(start.draft)
  const [current, setCurrent] = useState({ id: start.id, version: start.version })
  const [announcement, setAnnouncement] = useState('')
  const [issues, setIssues] = useState<string[]>([])
  const [serverIssues, setServerIssues] = useState<string[]>([])
  const [nameError, setNameError] = useState<string>()
  const [conflict, setConflict] = useState(false)
  const [gone, setGone] = useState(false)
  const [dialog, setDialog] = useState<DialogState>(null)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const bypass = useRef(false)
  const focusRequest = useRef<{ id: string; action: SlotAction } | null>(null)
  const issuesRef = useRef<HTMLUListElement>(null)

  const create = useCreateDashboard(siteId)
  const save = useSaveDashboard(siteId)
  const remove = useDeleteDashboard(siteId)
  const duplicate = useDuplicateDashboard(siteId)
  const setDefault = useSetDefaultDashboard(siteId)

  const dirty = isDirty(draft, baseline)
  const saving = create.isPending || save.isPending
  const saved = current.id === undefined ? undefined : dashboards.find((item) => item.id === current.id)
  const widgets = draft.layout.widgets
  const full = widgets.length >= limits.maxWidgets

  // Keep keyboard focus on the same control after a widget moves in the list.
  useLayoutEffect(() => {
    const request = focusRequest.current
    if (!request) return
    focusRequest.current = null
    const target = document.querySelector<HTMLElement>(`[data-widget-id="${CSS.escape(request.id)}"] [data-action="${request.action}"]`)
    target?.focus()
  }, [widgets])

  useEffect(() => {
    if (issues.length > 0 || serverIssues.length > 0) issuesRef.current?.focus()
  }, [issues, serverIssues])

  const context = useMemo(() => ({
    base: { ...base, filters: [] },
    timezone,
    compare: (draft.layout.compare ?? 'off') as Compare,
  }), [base, timezone, draft.layout.compare])
  const preview = {
    compare: context.compare,
    anonymous,
    onFilter: () => undefined,
    onShorten: () => undefined,
  }

  const act = (id: string, action: SlotAction) => {
    const index = widgets.findIndex((widget) => widget.id === id)
    const widget = widgets[index]
    if (!widget) return
    const name = widgetLabel(widget)
    focusRequest.current = { id, action }
    switch (action) {
      case 'up':
      case 'down': {
        const to = index + (action === 'up' ? -1 : 1)
        if (to < 0) return setAnnouncement(t.atStart(name))
        if (to >= widgets.length) return setAnnouncement(t.atEnd(name))
        dispatch({ type: 'move', id, delta: action === 'up' ? -1 : 1 })
        return setAnnouncement(t.moved(name, to + 1, widgets.length))
      }
      case 'narrower':
      case 'wider': {
        const next = widget.w + (action === 'wider' ? 1 : -1)
        const resized = reduceDraft(draft, { type: 'resize', id, w: next }).layout.widgets[index]
        if (resized.w === widget.w) return setAnnouncement(action === 'wider' ? t.atMaxWidth(name) : t.atMinWidth(name, resized.w))
        dispatch({ type: 'resize', id, w: next })
        return setAnnouncement(t.resized(name, resized.w))
      }
      case 'options':
        focusRequest.current = null
        return setDialog({ mode: 'edit', widget })
      case 'remove': {
        const neighbour = widgets[index + 1] ?? widgets[index - 1]
        focusRequest.current = neighbour ? { id: neighbour.id, action: 'options' } : null
        dispatch({ type: 'remove', id })
        return setAnnouncement(t.removed(name))
      }
    }
  }

  const resetMessages = () => { setIssues([]); setServerIssues([]); setNameError(undefined) }

  const applySaved = (dashboard: Dashboard, sent: Draft) => {
    setCurrent({ id: dashboard.id, version: dashboard.version })
    setBaseline({ name: sent.name.trim(), layout: sent.layout })
    setAnnouncement(t.savedAnnounce(dashboard.name))
  }

  const explain = (problem: Problem) => {
    const kind = conflictKind(problem)
    if (kind === 'stale') return setConflict(true)
    if (kind === 'name') return setNameError(t.nameTaken)
    if (kind === 'limit') return setServerIssues([t.limitReached(limits.maxDashboards)])
    if (problem?.status === 403) return setServerIssues([t.forbidden])
    const messages = problem?.status === 400 ? problemMessages(problem) : []
    setServerIssues(messages.length > 0 ? messages : [t.saveFailed])
  }

  const submit = async () => {
    resetMessages()
    const found = validateDraft(draft, issueText, widgetLabel)
    if (found.length > 0) {
      setIssues(found.map((issue) => issue.message))
      if (found.some((issue) => issue.field === 'name')) setNameError(t.nameInvalid)
      return
    }
    const body = { name: draft.name.trim(), layout: draft.layout }
    try {
      if (current.id === undefined) {
        const created = await create.mutateAsync(body)
        applySaved(created, draft)
        bypass.current = true
        onOpen(created)
      } else {
        const updated = await save.mutateAsync({ id: current.id, version: current.version ?? 0, ...body })
        applySaved(updated, draft)
      }
    } catch (error) {
      explain(error as Problem)
    }
  }

  const reload = async () => {
    const result = await refetch()
    const fresh = result.data?.find((item) => item.id === current.id && !item.builtin)
    if (!fresh) return setGone(true)
    const next = { name: fresh.name, layout: normalizeLayout(fresh.layout) }
    dispatch({ type: 'reset', draft: next })
    setBaseline(next)
    setCurrent({ id: fresh.id, version: fresh.version })
    setConflict(false)
    resetMessages()
    setAnnouncement(t.reloaded(fresh.name))
  }

  const saveCopy = async () => {
    const suffix = t.conflictCopySuffix
    const name = `${[...draft.name.trim()].slice(0, limits.maxName - suffix.length).join('')}${suffix}`
    try {
      const created = await create.mutateAsync({ name, layout: draft.layout })
      bypass.current = true
      onOpen(created)
    } catch (error) {
      explain(error as Problem)
    }
  }

  const doDuplicate = async () => {
    if (current.id === undefined) return
    try {
      const copy = await duplicate.mutateAsync(current.id)
      bypass.current = true
      onOpen(copy)
    } catch (error) {
      explain(error as Problem)
    }
  }

  const doDelete = async () => {
    if (current.id === undefined) return
    try {
      await remove.mutateAsync(current.id)
      bypass.current = true
      setConfirmDelete(false)
      onDeleted()
    } catch (error) {
      setConfirmDelete(false)
      explain(error as Problem)
    }
  }

  const toggleDefault = async () => {
    if (current.id === undefined) return
    try {
      await setDefault.mutateAsync(saved?.is_default ? undefined : current.id)
      setAnnouncement(saved?.is_default ? t.defaultCleared : t.defaultSet)
    } catch (error) {
      explain(error as Problem)
    }
  }

  const openAdd = () => setDialog({ mode: 'add', widget: makeWidget(draft.layout, 'kpi_strip') })

  const title = current.id === undefined ? t.headingNew : t.heading(baseline.name)
  const busy = saving || duplicate.isPending || remove.isPending || setDefault.isPending

  if (gone) {
    return (
      <section className="flex flex-col items-start gap-3 rounded-xl border bg-card p-5" role="status">
        <p className="text-sm">{t.gone}</p>
        <Button className="min-h-11 sm:min-h-9" type="button" variant="outline" onClick={() => { bypass.current = true; onDeleted() }}>{t.done}</Button>
      </section>
    )
  }

  return (
    <div className="dash-edit flex min-w-0 flex-col gap-4" data-editing={current.id ?? 'new'}>
      <UnsavedGuard active={dirty} bypass={bypass} />
      <p aria-live="polite" className="sr-only" role="status">{announcement}</p>

      <section aria-labelledby="dash-edit-title" className="dash-edit-bar flex flex-col gap-3 rounded-xl border p-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h2 className="min-w-0 break-words font-medium" id="dash-edit-title">{title}</h2>
          <p className="text-sm text-muted-foreground" data-testid="edit-status">
            {saving ? t.saving : dirty ? t.unsaved : current.id === undefined ? '' : t.saved}
          </p>
        </div>
        <Field data-invalid={nameError ? true : undefined}>
          <FieldLabel htmlFor="dash-edit-name">{t.nameLabel}</FieldLabel>
          <Input
            aria-describedby={nameError ? 'dash-edit-name-error' : undefined}
            aria-invalid={nameError ? true : undefined}
            className="min-h-11 sm:min-h-9"
            id="dash-edit-name"
            maxLength={120}
            value={draft.name}
            onChange={(event) => { setNameError(undefined); dispatch({ type: 'rename', name: event.target.value }) }}
          />
          {nameError ? <FieldError id="dash-edit-name-error">{nameError}</FieldError> : null}
        </Field>
        <div className="flex flex-wrap items-center gap-2">
          <Button className="min-h-11 sm:min-h-9" disabled={busy || conflict || (current.id !== undefined && !dirty)} type="button" onClick={() => void submit()}>
            {saving ? t.saving : current.id === undefined ? t.create : t.save}
          </Button>
          <Button className="min-h-11 sm:min-h-9" type="button" variant="outline" onClick={onExit}>
            {dirty ? t.discard : t.done}
          </Button>
        </div>
      </section>

      {conflict ? (
        <section className="flex flex-col items-start gap-3 rounded-xl border border-destructive/40 bg-card p-4" role="alert">
          <h2 className="font-medium">{t.conflictTitle}</h2>
          <p className="text-sm text-muted-foreground">{t.conflictBody}</p>
          <div className="flex flex-wrap gap-2">
            <Button className="min-h-11 sm:min-h-9" type="button" onClick={() => void reload()}>{t.conflictReload}</Button>
            <Button className="min-h-11 sm:min-h-9" disabled={busy} type="button" variant="outline" onClick={() => void saveCopy()}>{t.conflictCopy}</Button>
          </div>
        </section>
      ) : null}

      {issues.length > 0 || serverIssues.length > 0 ? (
        <section className="rounded-xl border border-destructive/40 bg-card p-4" role="alert">
          <h2 className="mb-2 font-medium">{serverIssues.length > 0 ? t.serverProblemsTitle : t.problemsTitle}</h2>
          <ul className="list-disc pl-5 text-sm text-destructive outline-none" ref={issuesRef} tabIndex={-1}>
            {[...issues, ...serverIssues].map((message, index) => <li key={`${message}-${index}`}>{message}</li>)}
          </ul>
        </section>
      ) : null}

      <DashboardSettings
        anonymous={anonymous}
        dateRange={base.dateRange}
        layout={draft.layout}
        orgId={orgId}
        siteId={siteId}
        onChange={(patch) => dispatch({ type: 'layout', patch })}
      />

      <section aria-labelledby="dash-edit-widgets" className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h2 className="text-sm font-medium" id="dash-edit-widgets">{t.widgets}</h2>
            <p className="text-xs text-muted-foreground">{t.count(widgets.length, limits.maxWidgets)}</p>
          </div>
          <Button className="min-h-11 sm:min-h-9" disabled={full} type="button" onClick={openAdd}>
            <PlusIcon />{t.add}
          </Button>
        </div>
        {full ? <p className="text-xs text-muted-foreground">{t.full(limits.maxWidgets)}</p> : null}
        <p className="text-xs text-muted-foreground">{t.previewNote} {t.shortcutHint}</p>
        {widgets.length === 0 ? (
          <div className="dash-edit-slot flex flex-col items-start gap-2 p-5" role="status">
            <h3 className="font-medium">{t.emptyTitle}</h3>
            <p className="text-sm text-muted-foreground">{t.emptyBody}</p>
          </div>
        ) : (
          <ol aria-label={en.dashView.widgetGrid} className="grid min-w-0 list-none grid-cols-1 items-start gap-4 p-0 md:grid-cols-2 lg:grid-cols-12">
            {widgets.map((widget, index) => (
              <WidgetSlot
                index={index}
                key={widget.id}
                preview={preview}
                query={widgetQuery(draft.layout, widget, [], { ...context })}
                total={widgets.length}
                widget={widget}
                onAction={act}
              />
            ))}
          </ol>
        )}
      </section>

      {current.id !== undefined ? (
        <section aria-labelledby="dash-edit-manage" className="flex flex-col gap-3 rounded-xl border bg-card p-4">
          <h2 className="text-sm font-medium" id="dash-edit-manage">{t.manage}</h2>
          <div className="flex flex-wrap items-center gap-2">
            <Button aria-describedby={dirty ? 'dash-edit-save-first' : undefined} className="min-h-11 sm:min-h-9" disabled={busy || dirty} type="button" variant="outline" onClick={() => void doDuplicate()}>{t.duplicate}</Button>
            <Button aria-pressed={saved?.is_default ?? false} className="min-h-11 sm:min-h-9" disabled={busy} type="button" variant="outline" onClick={() => void toggleDefault()}>
              {saved?.is_default ? t.clearDefault : t.makeDefault}
            </Button>
            <Button className="min-h-11 sm:min-h-9" disabled={busy} type="button" variant="destructive" onClick={() => setConfirmDelete(true)}>{t.delete}</Button>
          </div>
          {dirty ? <p className="text-xs text-muted-foreground" id="dash-edit-save-first">{t.saveFirst}</p> : null}
          {saved?.is_default ? <p className="text-xs text-muted-foreground">{t.isDefault}</p> : null}
        </section>
      ) : null}

      {dialog ? (
        <WidgetDialog
          key={dialog.widget.id + dialog.mode}
          mode={dialog.mode}
          siteId={siteId}
          widget={dialog.widget}
          onClose={() => setDialog(null)}
          onSubmit={(widget) => {
            setDialog(null)
            if (dialog.mode === 'add') {
              dispatch({ type: 'add', widget })
              focusRequest.current = { id: widget.id, action: 'options' }
              setAnnouncement(t.added(widgetLabel(widget), widgets.length + 1))
            } else {
              dispatch({ type: 'update', id: widget.id, patch: widget })
              focusRequest.current = { id: widget.id, action: 'options' }
            }
          }}
        />
      ) : null}

      <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t.deleteTitle(baseline.name)}</AlertDialogTitle>
            <AlertDialogDescription>{t.deleteBody}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel className="min-h-11 sm:min-h-9">{t.deleteKeep}</AlertDialogCancel>
            <AlertDialogAction className="min-h-11 sm:min-h-9" variant="destructive" onClick={(event) => { event.preventDefault(); void doDelete() }}>{t.deleteConfirm}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
