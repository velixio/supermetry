import { useEffect, useState } from 'react'
import { BookmarkIcon } from 'lucide-react'

import { ErrorState } from '@/components/ErrorState'
import { ListPager } from '@/components/ListPager'
import { ReplayLink } from '@/components/replay/ReplayLink'
import { ReplayOverlay, type ReplayOverlayRow } from '@/components/replay/ReplayOverlay'
import { BrowserLabel } from '@/components/visitor/BrowserLabel'
import { DeviceLabel } from '@/components/visitor/DeviceLabel'
import { Location } from '@/components/visitor/Location'
import { OsLabel } from '@/components/visitor/OsLabel'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Field, FieldGroup, FieldLabel, FieldSet, FieldLegend } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { useCapability, AccessDenied } from '@/lib/authorization'
import type { ReplayListItem, ReplayListQuery } from '@/lib/replay-api'
import { en } from '@/messages/en'

import { useReplays, useSetReplaySaved } from './api'
import type { ReplayLayout } from './search'

type ReplayFilters = Pick<ReplayListQuery, 'q' | 'min_duration' | 'max_duration' | 'has_events' | 'saved'>
type Paged<T> = T & Partial<{ total: number; total_capped: boolean }>

function formatDuration(seconds: number): string {
  if (seconds < 60) return `${Math.round(seconds)}s`
  return `${Math.floor(seconds / 60)}m ${Math.round(seconds % 60)}s`
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

function SavedButton({ replay, pending, onChange }: { replay: ReplayListItem; pending: boolean; onChange: (saved: boolean) => void }) {
  const label = replay.saved ? en.replays.unsave : en.replays.save
  return (
    <Button aria-label={label} aria-pressed={replay.saved} className="size-11" disabled={pending} size="icon" type="button" variant="ghost" onClick={() => onChange(!replay.saved)}>
      <BookmarkIcon aria-hidden="true" className={replay.saved ? 'fill-current' : undefined} />
    </Button>
  )
}

function ReplayCard({ replay, org, site, saving, onReplayChange, onSavedChange }: {
  replay: ReplayListItem
  org: string
  site: string
  saving: boolean
  onReplayChange?: (sessionId?: string, options?: { replace?: boolean }) => void
  onSavedChange: (saved: boolean) => void
}) {
  const started = formatDate(replay.started_at)
  return (
    <li aria-label={`${en.replays.started} ${started}`}>
      <Card className="gap-3 py-3">
        <CardHeader className="flex items-start justify-between gap-2">
          <CardTitle><time dateTime={replay.started_at}>{started}</time></CardTitle>
          <SavedButton onChange={onSavedChange} pending={saving} replay={replay} />
        </CardHeader>
        <CardContent className="flex min-w-0 flex-col gap-3 text-sm">
          <Location city={replay.city} country={replay.country} />
          <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
            <BrowserLabel value={replay.browser} /><OsLabel value={replay.os} /><DeviceLabel value={replay.device} />
          </div>
          <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs">
            <dt className="text-muted-foreground">{en.replays.actions}</dt><dd className="tabular-nums">{replay.actions}</dd>
            <dt className="text-muted-foreground">{en.replays.pages}</dt><dd className="tabular-nums">{replay.pages}</dd>
            <dt className="text-muted-foreground">{en.replays.trigger}</dt><dd className="[overflow-wrap:anywhere]">{replay.trigger || '—'}</dd>
          </dl>
        </CardContent>
        <CardFooter className="justify-between gap-3 text-xs text-muted-foreground">
          <span className="tabular-nums">{formatDuration(replay.duration_s)}</span>
          <div className="[&_[data-slot=button]]:size-11"><ReplayLink hasReplay org={org} site={site} sessionId={replay.session_id} onOpen={onReplayChange ? (id) => onReplayChange(id) : undefined} /></div>
        </CardFooter>
      </Card>
    </li>
  )
}

function ReplaysPageContent({ siteId, org, site, from, to, cursor, page, replayId, filters, layout, onFirst, onPrevious, onNext, onFiltersChange, onLayoutChange, onReplayChange }: {
  siteId: number; org: string; site: string; from: string; to: string; cursor?: string; page: number; replayId?: string
  filters: ReplayFilters; layout: ReplayLayout
  onFirst: () => void; onPrevious: () => void; onNext: (cursor: string) => void
  onFiltersChange: (patch: Partial<ReplayFilters>) => void; onLayoutChange: (layout: ReplayLayout) => void
  onReplayChange?: (sessionId?: string, options?: { replace?: boolean }) => void
}) {
  const [search, setSearch] = useState(filters.q ?? '')
  useEffect(() => setSearch(filters.q ?? ''), [filters.q])
  useEffect(() => {
    const next = search.trim()
    if (next === (filters.q ?? '')) return
    const timer = window.setTimeout(() => onFiltersChange({ q: next || undefined }), 300)
    return () => window.clearTimeout(timer)
  }, [filters.q, onFiltersChange, search])

  const replays = useReplays(siteId, { from, to, ...filters, cursor, limit: 50 })
  const saved = useSetReplaySaved(siteId)
  const data = replays.data as Paged<NonNullable<typeof replays.data>> | undefined
  const replayRows: ReplayOverlayRow[] = replays.data?.items.map((replay) => ({
    sessionId: replay.session_id, startedAt: replay.started_at, browser: replay.browser, os: replay.os, device: replay.device,
    city: replay.city, country: replay.country, durationMs: replay.duration_s * 1000, actions: replay.actions, pages: replay.pages,
  })) ?? []
  const hasFilters = Boolean(filters.q || filters.min_duration !== undefined || filters.max_duration !== undefined || filters.has_events !== undefined || filters.saved !== undefined)
  const updateDuration = (name: 'min_duration' | 'max_duration', raw: string) => {
    const number = raw === '' ? undefined : Number(raw)
    if (number === undefined || (Number.isInteger(number) && number >= 0 && number <= 31_536_000)) onFiltersChange({ [name]: number })
  }
  const changeSaved = (replay: ReplayListItem, next: boolean) => saved.mutate({ sessionId: replay.session_id, saved: next })

  return (
    <div className="flex min-w-0 max-w-full flex-col gap-6">
      <header className="flex flex-col gap-1"><h1 className="text-xl font-semibold tracking-tight">{en.replays.title}</h1><p className="text-sm text-muted-foreground">{en.replays.description}</p></header>
      <section aria-label={en.replays.filters} className="flex flex-col gap-3 rounded-xl border bg-card p-3">
        <div className="flex flex-col gap-3 lg:flex-row lg:items-end">
          <Field className="min-w-0 flex-1"><FieldLabel htmlFor="replay-search">{en.replays.search}</FieldLabel><Input className="h-11" id="replay-search" maxLength={200} role="searchbox" value={search} placeholder={en.replays.searchPlaceholder} onChange={(event) => setSearch(event.currentTarget.value)} /></Field>
          <FieldSet className="gap-1.5"><FieldLegend variant="label">{en.replays.view}</FieldLegend><ToggleGroup aria-label={en.replays.view} type="single" value={filters.saved ? 'saved' : 'all'} onValueChange={(value) => value && onFiltersChange({ saved: value === 'saved' ? true : undefined })}><ToggleGroupItem className="h-11" value="all">{en.replays.all}</ToggleGroupItem><ToggleGroupItem className="h-11" value="saved">{en.replays.saved}</ToggleGroupItem></ToggleGroup></FieldSet>
          <FieldSet className="hidden gap-1.5 md:flex"><FieldLegend variant="label">{en.replays.layout}</FieldLegend><ToggleGroup aria-label={en.replays.layout} type="single" value={layout} onValueChange={(value) => (value === 'table' || value === 'cards') && onLayoutChange(value)}><ToggleGroupItem className="h-11" value="table">{en.replays.table}</ToggleGroupItem><ToggleGroupItem className="h-11" value="cards">{en.replays.cards}</ToggleGroupItem></ToggleGroup></FieldSet>
        </div>
        <FieldGroup className="grid gap-3 sm:grid-cols-2 lg:grid-cols-[minmax(0,12rem)_minmax(0,12rem)_auto] lg:items-end">
          <Field><FieldLabel htmlFor="replay-min-duration">{en.replays.minimumDuration}</FieldLabel><Input className="h-11" id="replay-min-duration" inputMode="numeric" min={0} max={31_536_000} type="number" value={filters.min_duration ?? ''} onChange={(event) => updateDuration('min_duration', event.currentTarget.value)} /></Field>
          <Field><FieldLabel htmlFor="replay-max-duration">{en.replays.maximumDuration}</FieldLabel><Input className="h-11" id="replay-max-duration" inputMode="numeric" min={0} max={31_536_000} type="number" value={filters.max_duration ?? ''} onChange={(event) => updateDuration('max_duration', event.currentTarget.value)} /></Field>
          <FieldSet className="gap-1.5"><FieldLegend variant="label">{en.replays.events}</FieldLegend><ToggleGroup aria-label={en.replays.events} type="single" value={filters.has_events === undefined ? 'any' : filters.has_events ? 'yes' : 'no'} onValueChange={(value) => value && onFiltersChange({ has_events: value === 'any' ? undefined : value === 'yes' })}><ToggleGroupItem className="h-11" value="any">{en.replays.any}</ToggleGroupItem><ToggleGroupItem className="h-11" value="yes">{en.replays.hasEvents}</ToggleGroupItem><ToggleGroupItem className="h-11" value="no">{en.replays.noEvents}</ToggleGroupItem></ToggleGroup></FieldSet>
        </FieldGroup>
      </section>
      {saved.isError ? <p role="alert" className="text-sm text-destructive">{saved.error.message} {en.replays.saveRetry}</p> : null}
      {replays.isPending ? <Skeleton aria-label={en.replays.loading} className="h-64 w-full" /> : replays.isError ? <ErrorState detail={replays.error.message} onRetry={() => void replays.refetch()} /> : replays.data.items.length === 0 ? (
        <Empty><EmptyHeader><EmptyTitle>{filters.saved ? en.replays.noSaved : en.replays.noResults}</EmptyTitle><EmptyDescription>{filters.saved ? en.replays.noSavedDescription : hasFilters ? en.replays.noResultsFiltered : en.replays.noResultsDescription}</EmptyDescription></EmptyHeader>{hasFilters ? <Button type="button" variant="outline" onClick={() => onFiltersChange(filters.saved ? { saved: undefined } : { q: undefined, min_duration: undefined, max_duration: undefined, has_events: undefined, saved: undefined })}>{filters.saved ? en.replays.showAll : en.replays.clearFilters}</Button> : null}</Empty>
      ) : (
        <section className="flex min-w-0 max-w-full flex-col gap-4">
          <ol aria-label={en.replays.cardsLabel} className={layout === 'cards' ? 'flex flex-col gap-3 sm:grid sm:grid-cols-2' : 'flex flex-col gap-3 md:hidden'}>{replays.data.items.map((item) => <ReplayCard key={`${item.started_at}:${item.session_id}`} org={org} replay={item} saving={saved.isPending} site={site} onReplayChange={onReplayChange} onSavedChange={(next) => changeSaved(item, next)} />)}</ol>
          <div className={layout === 'table' ? 'hidden min-w-0 max-w-full overflow-hidden rounded-xl border bg-card md:block' : 'hidden'}>
            <Table aria-label={en.replays.tableLabel} className="min-w-[980px]"><TableHeader><TableRow><TableHead>{en.replays.saved}</TableHead><TableHead aria-label={en.replays.play}><span aria-hidden="true">▶</span></TableHead><TableHead>{en.replays.started}</TableHead><TableHead>{en.replays.location}</TableHead><TableHead>{en.replays.browser}</TableHead><TableHead>{en.replays.os}</TableHead><TableHead>{en.replays.device}</TableHead><TableHead>{en.replays.actions}</TableHead><TableHead>{en.replays.duration}</TableHead><TableHead>{en.replays.pages}</TableHead><TableHead>{en.replays.trigger}</TableHead></TableRow></TableHeader>
              <TableBody>{replays.data.items.map((item) => <TableRow key={`${item.started_at}:${item.session_id}`}><TableCell><SavedButton onChange={(next) => changeSaved(item, next)} pending={saved.isPending} replay={item} /></TableCell><TableCell><ReplayLink hasReplay org={org} site={site} sessionId={item.session_id} onOpen={onReplayChange ? (id) => onReplayChange(id) : undefined} /></TableCell><TableCell><time dateTime={item.started_at}>{formatDate(item.started_at)}</time></TableCell><TableCell><Location city={item.city} country={item.country} /></TableCell><TableCell><BrowserLabel value={item.browser} /></TableCell><TableCell><OsLabel value={item.os} /></TableCell><TableCell><DeviceLabel value={item.device} /></TableCell><TableCell className="tabular-nums">{item.actions}</TableCell><TableCell className="tabular-nums">{formatDuration(item.duration_s)}</TableCell><TableCell className="tabular-nums">{item.pages}</TableCell><TableCell>{item.trigger}</TableCell></TableRow>)}</TableBody>
            </Table>
          </div>
        </section>
      )}
      {!replays.isPending && !replays.isError ? <ListPager fetching={replays.isFetching} hasNext={Boolean(data?.next_cursor)} page={page} pageSize={50} rowCount={data?.items.length ?? 0} total={data?.total} totalCapped={data?.total_capped} onFirst={onFirst} onNext={() => data?.next_cursor && onNext(data.next_cursor)} onPrevious={onPrevious} /> : null}
      {onReplayChange ? <ReplayOverlay open={Boolean(replayId)} org={org} rows={replayRows} sessionId={replayId} site={site} siteId={siteId} onOpenChange={(open) => !open && onReplayChange(undefined, { replace: true })} onSessionChange={(id) => onReplayChange(id, { replace: true })} /> : null}
    </div>
  )
}

type ReplaysPageContentProps = Parameters<typeof ReplaysPageContent>[0]
type ReplaysPageProps = Omit<ReplaysPageContentProps, 'filters' | 'layout' | 'onFiltersChange' | 'onLayoutChange'> & Partial<Pick<ReplaysPageContentProps, 'filters' | 'layout' | 'onFiltersChange' | 'onLayoutChange'>>

const ignoreFilters: ReplaysPageContentProps['onFiltersChange'] = () => undefined
const ignoreLayout: ReplaysPageContentProps['onLayoutChange'] = () => undefined

export function ReplaysPage({ filters = {}, layout = 'table', onFiltersChange = ignoreFilters, onLayoutChange = ignoreLayout, ...props }: ReplaysPageProps) {
  const allowed = useCapability('replay.read')
  if (!allowed) return <AccessDenied subject="watch replays" />
  return <ReplaysPageContent {...props} filters={filters} layout={layout} onFiltersChange={onFiltersChange} onLayoutChange={onLayoutChange} />
}
