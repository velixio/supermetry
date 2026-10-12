import { Link } from '@tanstack/react-router'
import { CopyIcon, FilterIcon, PencilIcon, PlayIcon, PlusIcon, Trash2Icon, UsersRoundIcon } from 'lucide-react'
import { useState, type FormEvent } from 'react'

import { ErrorState } from '@/components/ErrorState'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription,
  AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { fmt } from '@/lib/format'
import { serializeFilters } from '@/lib/search'
import { en } from '@/messages/en'

import {
  useCohortEvaluation, useCohorts, useDeleteCohort, useDuplicateCohort, useSaveCohort,
  type EvaluationSubject,
} from './api'
import { ConditionBuilder } from './ConditionBuilder'
import {
  blankCondition, blankDraft, describeDefinition, draftFromDefinition, toDefinition, type DraftCohort,
} from './conditions'
import { PowerUsersPanel } from './PowerUsersPanel'
import type { Cohort, CohortDefinition } from './types'

const t = en.cohorts

type CohortsPageProps = {
  siteId: number
  orgSlug: string
  siteSlug: string
  canEdit: boolean
  dateRange: { from: string; to: string }
  // policy is why cohorts cannot run, or undefined when they can.
  policy?: 'anonymous' | 'profiles_disabled'
}

type Builder = { id?: number; draft: DraftCohort }

function formatDate(value: string): string {
  return value ? new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value)) : '—'
}

function ResultPanel({ siteId, subject, title }: { siteId: number; subject: EvaluationSubject; title: string }) {
  const evaluation = useCohortEvaluation(siteId, subject)
  return (
    <section aria-labelledby="cohort-result-title" className="flex min-w-0 flex-col gap-3 rounded-xl border bg-card p-4">
      <h2 className="text-base font-semibold" id="cohort-result-title">{t.resultTitle}: {title}</h2>
      <div aria-live="polite" role="status">
        {evaluation.isPending || evaluation.isFetching ? (
          <p className="text-sm text-muted-foreground">{t.evaluating}</p>
        ) : evaluation.isError ? (
          <p className="text-sm text-destructive" role="alert">{evaluation.error.message}</p>
        ) : (
          <div className="flex flex-col gap-1">
            <p className="text-sm text-muted-foreground">{t.sizeLabel}</p>
            <p className="text-3xl font-semibold tabular-nums">{fmt.number(evaluation.data.size)}{evaluation.data.size_capped ? '+' : ''}</p>
            {evaluation.data.size_capped ? <p className="text-xs text-muted-foreground">{t.sizeCapped(fmt.number(evaluation.data.size))}</p> : null}
            <p className="text-xs text-muted-foreground">{t.windowText(formatDate(evaluation.data.window_from), formatDate(evaluation.data.window_to))}</p>
          </div>
        )}
      </div>
      {evaluation.data && !evaluation.isFetching ? (
        evaluation.data.sample_withheld ? (
          <p className="text-sm text-muted-foreground">{t.sampleWithheld}</p>
        ) : evaluation.data.sample.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t.sampleEmpty}</p>
        ) : (
          <div className="min-w-0 overflow-x-auto rounded-lg border">
            <Table aria-label={t.sampleTitle}>
              <TableHeader>
                <TableRow>
                  <TableHead>{t.colExternalId}</TableHead>
                  <TableHead>{t.colLastSeen}</TableHead>
                  <TableHead className="text-right">{t.colSessions}</TableHead>
                  <TableHead className="text-right">{t.colEvents}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {evaluation.data.sample.map((member) => (
                  <TableRow key={member.id}>
                    <TableCell className="font-medium">{member.external_id}</TableCell>
                    <TableCell>{formatDate(member.last_seen_at)}</TableCell>
                    <TableCell className="text-right tabular-nums">{member.sessions}</TableCell>
                    <TableCell className="text-right tabular-nums">{member.events}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )
      ) : null}
    </section>
  )
}

function CohortsTab({
  siteId, orgSlug, siteSlug, canEdit, seed,
}: Pick<CohortsPageProps, 'siteId' | 'orgSlug' | 'siteSlug' | 'canEdit'> & { seed?: DraftCohort }) {
  const cohorts = useCohorts(siteId)
  const save = useSaveCohort(siteId)
  const duplicate = useDuplicateCohort(siteId)
  const remove = useDeleteCohort(siteId)
  const [builder, setBuilder] = useState<Builder | undefined>(() => (seed ? { draft: seed } : undefined))
  const [problems, setProblems] = useState<string[]>([])
  const [subject, setSubject] = useState<{ value: EvaluationSubject; title: string }>()
  const [deleting, setDeleting] = useState<Cohort>()
  const [nonce, setNonce] = useState(0)

  const open = (next: Builder) => {
    setProblems([])
    save.reset()
    setBuilder(next)
  }

  const validate = (): { name: string; definition: CohortDefinition } | undefined => {
    if (!builder) return undefined
    const result = toDefinition(builder.draft)
    const name = builder.draft.name.trim()
    const errors = [...result.errors]
    if (Array.from(name).length < 1 || Array.from(name).length > 80) errors.unshift(t.errors.name)
    setProblems(errors)
    return errors.length === 0 && result.definition ? { name, definition: result.definition } : undefined
  }

  const preview = () => {
    if (!builder) return
    const result = toDefinition(builder.draft)
    setProblems(result.errors)
    if (result.definition) {
      setNonce((value) => value + 1)
      setSubject({ value: { kind: 'draft', definition: result.definition, nonce: nonce + 1 }, title: builder.draft.name.trim() || t.builderTitleNew })
    }
  }

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const valid = validate()
    if (!valid || !builder) return
    try {
      const saved = await save.mutateAsync({ id: builder.id, ...valid })
      setBuilder(undefined)
      setSubject({ value: { kind: 'saved', id: saved.id }, title: saved.name })
    } catch {
      // The mutation error is shown next to the form.
    }
  }

  const confirmDelete = async () => {
    if (!deleting) return
    try {
      await remove.mutateAsync(deleting.id)
      if (subject?.value.kind === 'saved' && subject.value.id === deleting.id) setSubject(undefined)
      setDeleting(undefined)
    } catch {
      // The mutation error remains visible in the dialog.
    }
  }

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">{t.description}</p>
        {canEdit && !builder ? (
          <Button className="h-11 md:h-8" type="button" onClick={() => open({ draft: blankDraft() })}>
            <PlusIcon data-icon="inline-start" />
            {t.newCohort}
          </Button>
        ) : null}
      </div>

      {builder ? (
        <form aria-labelledby="cohort-builder-title" className="flex min-w-0 flex-col gap-4 rounded-xl border bg-card p-4" noValidate onSubmit={submit}>
          <h2 className="text-base font-semibold" id="cohort-builder-title">{builder.id ? t.builderTitleEdit : t.builderTitleNew}</h2>
          <ConditionBuilder draft={builder.draft} onChange={(draft) => setBuilder({ ...builder, draft })} />
          {problems.length > 0 || save.error ? (
            <ul className="list-disc pl-5 text-sm text-destructive" role="alert">
              {problems.map((problem) => <li key={problem}>{problem}</li>)}
              {save.error ? <li>{save.error.message}</li> : null}
            </ul>
          ) : null}
          <div className="flex flex-wrap gap-2">
            <Button className="h-11 md:h-8" type="button" variant="outline" onClick={preview}>
              <PlayIcon data-icon="inline-start" />
              {t.previewSize}
            </Button>
            <Button className="h-11 md:h-8" disabled={save.isPending} type="submit">{save.isPending ? t.saving : t.save}</Button>
            <Button className="h-11 md:h-8" type="button" variant="ghost" onClick={() => { setBuilder(undefined); setProblems([]) }}>{t.cancel}</Button>
          </div>
        </form>
      ) : null}

      {subject ? <ResultPanel siteId={siteId} subject={subject.value} title={subject.title} /> : null}

      {cohorts.isError ? (
        <ErrorState detail={cohorts.error.message} onRetry={() => void cohorts.refetch()} />
      ) : cohorts.isPending ? (
        <div aria-label={t.loading} className="flex flex-col gap-3">
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-24 w-full" />
        </div>
      ) : cohorts.data.length === 0 ? (
        <Empty>
          <UsersRoundIcon aria-hidden="true" />
          <EmptyHeader>
            <EmptyTitle>{t.empty}</EmptyTitle>
            <EmptyDescription>{t.emptyDescription}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <div className="min-w-0 overflow-x-auto rounded-xl border bg-card">
          <Table aria-label={t.title}>
            <TableHeader>
              <TableRow>
                <TableHead>{t.colName}</TableHead>
                <TableHead className="hidden sm:table-cell">{t.colWindow}</TableHead>
                <TableHead>{t.colDefinition}</TableHead>
                <TableHead>{t.colActions}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {cohorts.data.map((cohort) => (
                <TableRow key={cohort.id}>
                  <TableCell className="min-w-24 whitespace-normal font-medium">{cohort.name}</TableCell>
                  <TableCell className="hidden whitespace-nowrap sm:table-cell">{t.windowValue(cohort.definition.window_days)}</TableCell>
                  <TableCell className="max-w-md whitespace-normal text-sm text-muted-foreground">{describeDefinition(cohort.definition)}</TableCell>
                  <TableCell className="max-w-48 sm:max-w-none">
                    <div className="flex flex-wrap gap-1">
                      <Button aria-label={t.actionEvaluate(cohort.name)} className="h-11 md:h-8" type="button" variant="outline" onClick={() => setSubject({ value: { kind: 'saved', id: cohort.id }, title: cohort.name })}>
                        <PlayIcon data-icon="inline-start" />
                        {t.evaluate}
                      </Button>
                      <Button asChild aria-label={t.actionFilter(cohort.name)} className="h-11 md:h-8" variant="outline">
                        <Link
                          params={{ org: orgSlug, site: siteSlug }}
                          search={{ period: '30d', compare: 'off', f: serializeFilters([{ dimension: 'cohort', operator: 'is', values: [String(cohort.id)] }]), metric: 'visitors', include_imported: false }}
                          to="/app/$org/$site"
                        >
                          <FilterIcon data-icon="inline-start" />
                          {t.useAsFilter}
                        </Link>
                      </Button>
                      {canEdit ? <>
                        <Button aria-label={t.actionEdit(cohort.name)} className="size-11 md:size-8" size="icon" type="button" variant="ghost" onClick={() => open({ id: cohort.id, draft: draftFromDefinition(cohort.name, cohort.definition) })}>
                          <PencilIcon />
                        </Button>
                        <Button aria-label={t.actionDuplicate(cohort.name)} className="size-11 md:size-8" disabled={duplicate.isPending} size="icon" type="button" variant="ghost" onClick={() => duplicate.mutate(cohort.id)}>
                          <CopyIcon />
                        </Button>
                        <Button aria-label={t.actionDelete(cohort.name)} className="size-11 md:size-8" size="icon" type="button" variant="ghost" onClick={() => setDeleting(cohort)}>
                          <Trash2Icon />
                        </Button>
                      </> : null}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {duplicate.isError ? <p className="text-sm text-destructive" role="alert">{duplicate.error.message}</p> : null}

      <AlertDialog open={Boolean(deleting)} onOpenChange={(openState) => !openState && setDeleting(undefined)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t.deleteTitle}</AlertDialogTitle>
            <AlertDialogDescription>{t.deleteBody(deleting?.name ?? '')}</AlertDialogDescription>
          </AlertDialogHeader>
          {remove.isError ? <p role="alert">{remove.error.message}</p> : null}
          <AlertDialogFooter>
            <AlertDialogCancel>{t.cancel}</AlertDialogCancel>
            <AlertDialogAction disabled={remove.isPending} variant="destructive" onClick={(event) => { event.preventDefault(); void confirmDelete() }}>
              {remove.isPending ? t.deleting : t.deleteConfirm}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

export function CohortsPage({ siteId, orgSlug, siteSlug, canEdit, dateRange, policy }: CohortsPageProps) {
  const [tab, setTab] = useState<'cohorts' | 'power'>('cohorts')
  const [seed, setSeed] = useState<DraftCohort>()

  if (policy) {
    return (
      <section aria-labelledby="cohorts-unavailable" className="flex max-w-2xl flex-col items-start gap-3 py-6" role="status">
        <h1 className="text-xl font-semibold tracking-tight" id="cohorts-unavailable">
          {policy === 'anonymous' ? t.anonymousTitle : t.profilesOffTitle}
        </h1>
        <p className="text-sm leading-relaxed text-muted-foreground">{policy === 'anonymous' ? t.anonymousBody : t.profilesOffBody}</p>
      </section>
    )
  }

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <header>
        <h1 className="text-xl font-semibold tracking-tight">{t.title}</h1>
      </header>
      <Tabs value={tab} onValueChange={(value) => { setSeed(undefined); setTab(value as 'cohorts' | 'power') }}>
        <TabsList aria-label={t.title}>
          <TabsTrigger className="h-9 px-4 md:h-7" value="cohorts">{t.tabCohorts}</TabsTrigger>
          <TabsTrigger className="h-9 px-4 md:h-7" value="power">{t.tabPower}</TabsTrigger>
        </TabsList>
        <TabsContent className="mt-4" value="cohorts">
          <CohortsTab canEdit={canEdit} orgSlug={orgSlug} seed={seed} siteId={siteId} siteSlug={siteSlug} />
        </TabsContent>
        <TabsContent className="mt-4" value="power">
          <PowerUsersPanel
            canEdit={canEdit}
            dateRange={dateRange}
            siteId={siteId}
            onSaveAsCohort={(metric, min) => {
              const draft = blankDraft()
              draft.conditions = [{ ...blankCondition('activity'), metric, minCount: String(min) }]
              draft.windowDays = String(Math.min(90, Math.max(1, daysBetween(dateRange))))
              setSeed(draft)
              setTab('cohorts')
            }}
          />
        </TabsContent>
      </Tabs>
    </div>
  )
}

function daysBetween(range: { from: string; to: string }): number {
  const from = Date.parse(`${range.from}T00:00:00Z`)
  const to = Date.parse(`${range.to}T00:00:00Z`)
  return Number.isFinite(from) && Number.isFinite(to) ? Math.round((to - from) / 86_400_000) + 1 : 30
}
