import { Link } from '@tanstack/react-router'
import { useEffect, useState } from 'react'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { fmt } from '@/lib/format'

import { AppFilters } from './AppFilters'
import { IssueBulkBar } from './IssueBulkBar'
import { IssueSearchBar } from './IssueSearchBar'
import { useAppIssues, type AppIssueRow } from './api'
import { formatDay } from './format'
import { appQuery, issueSearchQuery, ISSUE_KINDS, ISSUE_SORTS, ISSUE_STATUSES, type AppIssuesSearch, type AppSearch, type IssueSort, type IssueStatusTab } from './search'
import { Sparkline } from './Sparkline'

const PAGE_SIZE = 50
const ALL = '__all__'
const STATUS_LABELS: Record<IssueStatusTab, string> = { open: 'Open', regressed: 'Regressed', resolved: 'Resolved', ignored: 'Ignored', archived: 'Archived', all: 'All' }
const SORT_LABELS: Record<IssueSort, string> = { last_seen: 'Last seen', events: 'Events', users: 'Users' }

export function StatusBadge({ status }: { status: string }) {
  const variant = status === 'regressed' ? 'destructive' : status === 'open' ? 'default' : 'secondary'
  return <Badge variant={variant}>{status}</Badge>
}

/** Row checkboxes of the issues page: which ids are checked and how to change that. */
export type IssueSelection = { selected: ReadonlySet<number>; onToggle: (id: number) => void; onToggleAll: (all: boolean) => void }

/** Issue rows shared by the overview's top issues and the issues page. */
export function IssueRowsTable({ org, site, issues, search, selection }: { org: string; site: string; issues: AppIssueRow[]; search: AppSearch; selection?: IssueSelection }) {
  if (issues.length === 0) return <p className="rounded-lg bg-muted/30 p-8 text-center text-sm text-muted-foreground">No issues here.</p>
  return (
    <Table>
      <TableHeader>
        <TableRow>
          {selection ? (
            <TableHead className="w-8">
              <Checkbox aria-label="Select all issues on this page" checked={issues.every((issue) => selection.selected.has(issue.id))} onCheckedChange={(checked) => selection.onToggleAll(checked === true)} />
            </TableHead>
          ) : null}
          <TableHead>Trend</TableHead><TableHead>Issue</TableHead><TableHead>First / last seen</TableHead>
          <TableHead>Releases</TableHead><TableHead className="text-right">Events</TableHead><TableHead className="text-right">Users</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {issues.map((issue) => (
          <TableRow key={issue.id} data-state={selection?.selected.has(issue.id) ? 'selected' : undefined}>
            {selection ? <TableCell><Checkbox aria-label={`Select ${issue.title}`} checked={selection.selected.has(issue.id)} onCheckedChange={() => selection.onToggle(issue.id)} /></TableCell> : null}
            <TableCell><Sparkline values={issue.sparkline} /></TableCell>
            <TableCell className="max-w-md whitespace-normal">
              <div className="flex flex-wrap items-center gap-2">
                <Link className="font-medium underline-offset-2 hover:underline" to="/app/$org/$site/issues/$issue" params={{ org, site, issue: String(issue.id) }} search={{ period: search.period, release: search.release, environment: search.environment, platform: search.platform }}>{issue.title}</Link>
                <StatusBadge status={issue.status} />
              </div>
              <div className="text-xs text-muted-foreground"><span>{issue.error_type}</span> · {issue.kind}</div>
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">{formatDay(issue.first_seen)} / {formatDay(issue.last_seen)}</TableCell>
            <TableCell className="text-xs">{issue.first_release === issue.last_release ? issue.last_release : `${issue.first_release} → ${issue.last_release}`}</TableCell>
            <TableCell className="text-right tabular-nums">{fmt.number(issue.events)}</TableCell>
            <TableCell className="text-right tabular-nums">{fmt.number(issue.users)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

export function IssuesTable({ org, site, siteId, timezone, search, onSearch, canEdit = false }: {
  org: string; site: string; siteId: number; timezone: string; search: AppIssuesSearch; onSearch: (next: AppIssuesSearch) => void; canEdit?: boolean
}) {
  const offset = search.offset ?? 0
  const filters = { status: search.status === 'all' ? undefined : search.status, kind: search.kind, q: search.q, query: issueSearchQuery(search) }
  const issues = useAppIssues(siteId, search, timezone, { ...filters, sort: search.sort, limit: PAGE_SIZE, offset: offset || undefined })
  const [query, setQuery] = useState(search.q ?? '')
  const [selected, setSelected] = useState<ReadonlySet<number>>(new Set())
  const [allMatching, setAllMatching] = useState(false)
  const [bulkNote, setBulkNote] = useState('')
  // A different list (filters, search or page) starts with nothing checked.
  const listKey = appQuery(search, timezone, { ...filters, offset: offset || undefined }).toString()
  useEffect(() => { setSelected(new Set()); setAllMatching(false) }, [listKey])
  const rows = issues.data?.issues ?? []
  const total = issues.data?.total ?? 0
  const selection: IssueSelection | undefined = canEdit ? {
    selected,
    onToggle: (id) => { setBulkNote(''); setAllMatching(false); setSelected((current) => { const next = new Set(current); if (!next.delete(id)) next.add(id); return next }) },
    onToggleAll: (all) => { setAllMatching(false); setSelected(all ? new Set(rows.map((row) => row.id)) : new Set()) },
  } : undefined
  const matchTarget = { query: issueSearchQuery(search) ?? '', params: appQuery(search, timezone, filters).toString() }
  // Any filter change returns to the first page.
  const change = (patch: Partial<AppIssuesSearch>) => onSearch({ ...search, offset: undefined, ...patch })
  const nextOffset = issues.data?.next_offset ?? null
  return (
    <PageContainer>
      <PageHeader title="Issues" description="Grouped crashes and errors." actions={<AppFilters siteId={siteId} timezone={timezone} search={search} showProcess onSearch={(next) => change(next)} />} />
      <div className="flex flex-wrap items-center justify-between gap-3">
        <Tabs value={search.status} onValueChange={(value) => change({ status: value as IssueStatusTab })}>
          <TabsList>{ISSUE_STATUSES.map((status) => <TabsTrigger key={status} value={status}>{STATUS_LABELS[status]}</TabsTrigger>)}</TabsList>
        </Tabs>
        <div className="flex flex-wrap gap-2">
          <Select value={search.kind ?? ALL} onValueChange={(value) => change({ kind: value === ALL ? undefined : value })}>
            <SelectTrigger aria-label="Kind"><SelectValue /></SelectTrigger>
            <SelectContent><SelectItem value={ALL}>All kinds</SelectItem>{ISSUE_KINDS.map((kind) => <SelectItem key={kind} value={kind}>{kind}</SelectItem>)}</SelectContent>
          </Select>
          <Select value={search.sort} onValueChange={(value) => change({ sort: value as IssueSort })}>
            <SelectTrigger aria-label="Sort by"><SelectValue /></SelectTrigger>
            <SelectContent>{ISSUE_SORTS.map((sort) => <SelectItem key={sort} value={sort}>{SORT_LABELS[sort]}</SelectItem>)}</SelectContent>
          </Select>
          <form role="search" onSubmit={(event) => { event.preventDefault(); change({ q: query.trim() || undefined }) }}>
            <Input aria-label="Search issues" className="w-48" placeholder="Search title or type" value={query} onChange={(event) => setQuery(event.target.value)} />
          </form>
        </div>
      </div>
      <IssueSearchBar key={search.query ?? ''} siteId={siteId} value={search.query ?? ''} error={issues.error} canEdit={canEdit} onSubmit={(next) => change({ query: next || undefined })} />
      {selection && selected.size > 0 ? (
        <>
          <IssueBulkBar siteId={siteId} count={allMatching ? total : selected.size} matching={allMatching} target={allMatching ? matchTarget : { ids: [...selected] }} onDone={(summary) => { setBulkNote(summary); setSelected(new Set()); setAllMatching(false) }} />
          {!allMatching && selected.size === rows.length && total > rows.length ? (
            <p className="text-sm text-muted-foreground">
              These {rows.length} issues are selected. <button type="button" className="underline" onClick={() => setAllMatching(true)}>Select all {fmt.number(total)} matching issues</button>
            </p>
          ) : null}
        </>
      ) : null}
      {bulkNote && selected.size === 0 ? <p role="status" className="text-sm text-muted-foreground">{bulkNote}</p> : null}
      {issues.isPending ? <p role="status" className="text-sm text-muted-foreground">Loading issues…</p> : null}
      {issues.data ? <IssueRowsTable org={org} site={site} issues={issues.data.issues} search={search} selection={selection} /> : null}
      {issues.data ? (
        <div className="flex items-center justify-between text-sm text-muted-foreground">
          <span>{issues.data.total === 0 ? '0 issues' : `${offset + 1}–${offset + issues.data.issues.length} of ${fmt.number(issues.data.total)}`}</span>
          <div className="flex gap-2">
            <Button variant="outline" size="sm" disabled={offset === 0} onClick={() => onSearch({ ...search, offset: Math.max(0, offset - PAGE_SIZE) || undefined })}>Previous page</Button>
            <Button variant="outline" size="sm" disabled={nextOffset === null} onClick={() => onSearch({ ...search, offset: nextOffset ?? undefined })}>Next page</Button>
          </div>
        </div>
      ) : null}
    </PageContainer>
  )
}
