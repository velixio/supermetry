import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { useDeferredValue, useEffect, useId, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { EllipsisIcon, LayoutGridIcon, ListIcon, PlusIcon } from 'lucide-react'

import { ErrorState } from '@/components/ErrorState'
import { SiteIcon } from '@/components/SiteIcon'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { copyText } from '@/lib/clipboard'
import { fmt } from '@/lib/format'
import { en } from '@/messages/en'

import { deleteSite, listSitesOverview, type OverviewPeriod, type OverviewSite } from './api'
import { InstallStatusView } from './InstallStatus'
import type { SitesOverviewSort, SitesOverviewView } from './overviewSearch'
import { privateRequest } from '@/features/settings/privateApi'
import { activeLocale } from '@/lib/i18n/intl'
import { defaultSearch } from '@/lib/search'

export type { OverviewSite } from './api'
export type { SitesOverviewSort, SitesOverviewView } from './overviewSearch'

export const sitesOverviewKey = (period: OverviewPeriod) => ['sites', 'overview', { period, limit: 200 }] as const

function changeLabel(site: OverviewSite): string {
  if (site.change_percent === null) return 'No previous-period comparison'
  const prefix = site.change_percent > 0 ? '+' : ''
  return `${prefix}${fmt.percent(site.change_percent)} vs previous period`
}

function lastEvent(site: OverviewSite): string {
  if (!site.last_event_at) return 'No accepted events'
  return new Intl.DateTimeFormat(activeLocale(), { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(site.last_event_at))
}

function Sparkline({ site }: { site: OverviewSite }) {
  const descriptionId = useId()
  const values = site.sparkline.map((point) => point.visitors)
  const high = Math.max(1, ...values)
  const low = values.length > 0 ? Math.min(...values) : 0
  const peak = values.length > 0 ? Math.max(...values) : 0
  const points = values.map((value, index) => {
    const x = values.length <= 1 ? 50 : (index / (values.length - 1)) * 100
    const y = 30 - (value / high) * 26
    return `${x},${y}`
  }).join(' ')
  const summary = `Low ${low} visitors; peak ${peak} visitors across ${values.length} traffic buckets.`
  return (
    <div className="h-9 w-full" role="img" aria-label={`Traffic for ${site.name}`} aria-describedby={descriptionId}>
      <svg aria-hidden="true" className="size-full" preserveAspectRatio="none" viewBox="0 0 100 32">
        <polyline fill="none" points={points} stroke="var(--chart-1)" strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" vectorEffect="non-scaling-stroke" />
      </svg>
      <span className="sr-only" id={descriptionId}>{summary}</span>
    </div>
  )
}

function ChangeBadge({ site }: { site: OverviewSite }) {
  return <Badge variant={site.change_percent === null ? 'outline' : 'secondary'}>{changeLabel(site)}</Badge>
}

function SiteActions({ site, onDelete }: { site: OverviewSite; onDelete(site: OverviewSite): void }) {
  const canEdit = site.role !== 'viewer'
  const canDelete = site.role === 'owner' || site.role === 'admin'
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button type="button" size="icon" variant="ghost" aria-label={`Actions for ${site.name}`}><EllipsisIcon /></Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuGroup>
          <DropdownMenuItem asChild><Link to="/app/$org/$site" params={{ org: site.organization.slug, site: site.slug ?? site.public_id }} search={defaultSearch()}>Open dashboard</Link></DropdownMenuItem>
          {canEdit ? <DropdownMenuItem asChild><Link to="/app/$org/$site/settings/general" params={{ org: site.organization.slug, site: site.slug ?? site.public_id }}>Site settings</Link></DropdownMenuItem> : null}
          <DropdownMenuItem asChild><Link to="/app/$org/$site/settings/installation" params={{ org: site.organization.slug, site: site.slug ?? site.public_id }}>Installation</Link></DropdownMenuItem>
          <DropdownMenuItem onSelect={() => void copyText(site.tracking_snippet)}>Copy tracking snippet</DropdownMenuItem>
          {canDelete ? <DropdownMenuItem variant="destructive" onSelect={() => onDelete(site)}>Delete site</DropdownMenuItem> : null}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function SiteCard({ site, multipleOrganizations, onDelete }: { site: OverviewSite; multipleOrganizations: boolean; onDelete(site: OverviewSite): void }) {
  const accessibleName = `${site.name}, ${site.visitors} visitors, ${site.live_visitors} live, ${changeLabel(site)}`
  return (
    <Card data-testid="site-overview-card" className="h-72 content-auto [contain-intrinsic-size:288px]">
      <CardHeader className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-3">
        <SiteIcon iconUrl={site.icon_url} name={site.name} size={40} />
        <div className="min-w-0">
          <CardTitle className="text-base"><Link aria-label={accessibleName} className="outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring" to="/app/$org/$site" params={{ org: site.organization.slug, site: site.slug ?? site.public_id }} search={defaultSearch()}>{site.name}</Link></CardTitle>
          <p className="truncate text-sm text-muted-foreground">{site.domain}</p>
          {multipleOrganizations ? <p className="truncate text-xs text-muted-foreground">{site.organization.name} · {site.role}</p> : <p className="text-xs capitalize text-muted-foreground">{site.role}</p>}
          <p className="text-xs text-muted-foreground">Created {fmt.date(site.created_at, site.timezone)}</p>
        </div>
        <SiteActions site={site} onDelete={onDelete} />
      </CardHeader>
      <CardContent className="grid grid-cols-2 gap-4">
        <div><p className="text-xs text-muted-foreground">Visitors</p><p className="text-2xl font-semibold tabular-nums">{fmt.number(site.visitors)}</p></div>
        <div><p className="text-xs text-muted-foreground">Live</p><p className="text-2xl font-semibold tabular-nums">{fmt.number(site.live_visitors)}</p></div>
        <div className="col-span-2"><Sparkline site={site} /></div>
      </CardContent>
      <CardFooter className="mt-auto flex-wrap justify-between gap-2">
        <ChangeBadge site={site} />
        <InstallStatusView status={site.install_status} lastSeenAt={site.last_event_at} />
      </CardFooter>
    </Card>
  )
}

function SitesTable({ sites, multipleOrganizations, onDelete }: { sites: OverviewSite[]; multipleOrganizations: boolean; onDelete(site: OverviewSite): void }) {
  return (
    <div className="rounded-xl border bg-card">
      <Table aria-label="Sites overview"><TableHeader><TableRow>
        <TableHead>Site</TableHead>{multipleOrganizations ? <TableHead>Organization</TableHead> : null}<TableHead>Created</TableHead><TableHead>Visitors</TableHead><TableHead>Change</TableHead><TableHead>Traffic</TableHead><TableHead>Live</TableHead><TableHead>Last event</TableHead><TableHead>Status</TableHead><TableHead><span className="sr-only">Actions</span></TableHead>
      </TableRow></TableHeader><TableBody>{sites.map((site) => (
        <TableRow className="h-20" key={site.id}>
          <TableCell><div className="flex items-center gap-3"><SiteIcon iconUrl={site.icon_url} name={site.name} size={32} /><div className="min-w-0"><Link className="font-medium hover:underline" to="/app/$org/$site" params={{ org: site.organization.slug, site: site.slug ?? site.public_id }} search={defaultSearch()}>{site.name}</Link><p className="max-w-52 truncate text-xs text-muted-foreground">{site.domain}</p></div></div></TableCell>
          {multipleOrganizations ? <TableCell>{site.organization.name}<p className="text-xs capitalize text-muted-foreground">{site.role}</p></TableCell> : null}
          <TableCell>{fmt.date(site.created_at, site.timezone)}</TableCell><TableCell className="tabular-nums">{fmt.number(site.visitors)}</TableCell><TableCell><ChangeBadge site={site} /></TableCell><TableCell className="w-36"><Sparkline site={site} /></TableCell><TableCell className="tabular-nums">{fmt.number(site.live_visitors)}</TableCell><TableCell>{lastEvent(site)}</TableCell><TableCell><InstallStatusView status={site.install_status} lastSeenAt={site.last_event_at} /></TableCell><TableCell><SiteActions site={site} onDelete={onDelete} /></TableCell>
        </TableRow>
      ))}</TableBody></Table>
    </div>
  )
}

export function SitesOverview({ canAdd, orgs, org = '', period, query, sort, view, onSearchChange, onPeriodChange, onSortChange, onOrgChange, onViewChange }: {
  canAdd: boolean
  orgs: Array<{ id: number; name: string; slug: string; canCreate?: boolean }>
  org?: string
  period: OverviewPeriod
  query: string
  sort: SitesOverviewSort
  view: SitesOverviewView
  onSearchChange(value: string): void
  onPeriodChange(value: OverviewPeriod): void
  onSortChange(value: SitesOverviewSort): void
  onOrgChange(value: string): void
  onViewChange?(value: SitesOverviewView): void
}) {
  const [enabled, setEnabled] = useState(false)
  const [pendingDelete, setPendingDelete] = useState<OverviewSite>()
  const deferredQuery = useDeferredValue(query.trim().toLocaleLowerCase())
  const queryClient = useQueryClient()
  useEffect(() => setEnabled(true), [])
  const overview = useQuery({ queryKey: sitesOverviewKey(period), queryFn: () => listSitesOverview(period), enabled })
  const deletion = useMutation({ mutationFn: (site: OverviewSite) => deleteSite(site.id, site.org_id), onSuccess: () => queryClient.invalidateQueries({ queryKey: ['sites', 'overview'] }) })
  const viewPreference = useMutation({
    mutationFn: (sitesView: SitesOverviewView) => privateRequest('PATCH', '/api/private/me', { body: { preferences: { sites_view: sitesView } } }),
    onMutate: (sitesView) => {
      onViewChange?.(sitesView)
      queryClient.setQueryData<Record<string, unknown>>(['me'], (current) => current ? {
        ...current,
        preferences: { landing_page: 'overview', ...(current.preferences as object | undefined), sites_view: sitesView },
      } : current)
    },
  })
  const filtered = useMemo(() => {
    const rows = (overview.data?.data ?? []).filter((site) => (!org || site.organization.slug === org) && (!deferredQuery || `${site.name} ${site.domain}`.toLocaleLowerCase().includes(deferredQuery)))
    return [...rows].sort((left, right) => {
      if (sort === 'name') return left.name.localeCompare(right.name)
      if (sort === 'created') return new Date(right.created_at).getTime() - new Date(left.created_at).getTime()
      if (sort === 'last_event') return (right.last_event_at ?? '').localeCompare(left.last_event_at ?? '')
      if (sort === 'change') return (right.change_percent ?? -Infinity) - (left.change_percent ?? -Infinity)
      return right.visitors - left.visitors
    })
  }, [deferredQuery, org, overview.data?.data, sort])
  const multipleOrganizations = new Set((overview.data?.data ?? []).map((site) => site.org_id)).size > 1
  const addOrg = orgs.find((item) => item.slug === org && item.canCreate !== false) ?? orgs.find((item) => item.canCreate !== false)

  return (
    <PageContainer>
      <PageHeader
        title={en.sitesOverview.title}
        description={en.sitesOverview.description}
        actions={canAdd && addOrg ? <Button asChild><Link to="/app/$org/sites/new" params={{ org: addOrg.slug }}><PlusIcon data-icon="inline-start" />Add site</Link></Button> : undefined}
      />
      <div className="flex flex-wrap items-center gap-2">
        <Input className="min-w-48 flex-1 sm:max-w-xs" aria-label="Search sites" type="search" placeholder="Search name or domain" value={query} onChange={(event) => onSearchChange(event.target.value)} />
        {orgs.length > 1 ? <Select value={org || 'all'} onValueChange={(value) => onOrgChange(value === 'all' ? '' : value)}><SelectTrigger aria-label="Organization"><SelectValue /></SelectTrigger><SelectContent><SelectGroup><SelectItem value="all">All organizations</SelectItem>{orgs.map((item) => <SelectItem key={item.id} value={item.slug}>{item.name}</SelectItem>)}</SelectGroup></SelectContent></Select> : null}
        <Select value={period} onValueChange={(value) => onPeriodChange(value as OverviewPeriod)}><SelectTrigger aria-label="Period"><SelectValue /></SelectTrigger><SelectContent><SelectGroup><SelectItem value="24h">24 hours</SelectItem><SelectItem value="7d">7 days</SelectItem><SelectItem value="30d">30 days</SelectItem></SelectGroup></SelectContent></Select>
        <Select value={sort} onValueChange={(value) => onSortChange(value as SitesOverviewSort)}><SelectTrigger aria-label="Sort sites"><SelectValue /></SelectTrigger><SelectContent><SelectGroup><SelectItem value="visitors">Most visitors</SelectItem><SelectItem value="change">Biggest change</SelectItem><SelectItem value="name">Name</SelectItem><SelectItem value="created">Newest</SelectItem><SelectItem value="last_event">Latest event</SelectItem></SelectGroup></SelectContent></Select>
        <ToggleGroup aria-label="Site view" type="single" value={view} onValueChange={(value) => { if (value) viewPreference.mutate(value as SitesOverviewView) }}><ToggleGroupItem value="cards" aria-label="Card view"><LayoutGridIcon /></ToggleGroupItem><ToggleGroupItem value="table" aria-label="Table view"><ListIcon /></ToggleGroupItem></ToggleGroup>
      </div>
      {!enabled || overview.isPending ? <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3" role="status" aria-label="Loading sites">{Array.from({ length: 6 }, (_, index) => <Skeleton className="h-72 rounded-xl" key={index} />)}</div>
        : overview.isError ? <ErrorState detail={overview.error.message} onRetry={() => void overview.refetch()} />
          : (overview.data?.data.length ?? 0) === 0 ? <Empty><EmptyHeader><EmptyTitle>No sites yet</EmptyTitle><EmptyDescription>Add a site to start collecting analytics.</EmptyDescription></EmptyHeader>{canAdd && addOrg ? <Button asChild><Link to="/app/$org/sites/new" params={{ org: addOrg.slug }}>Add your first site</Link></Button> : null}</Empty>
            : filtered.length === 0 ? <Empty><EmptyHeader><EmptyTitle>No sites match these filters.</EmptyTitle><EmptyDescription>Change the search or organization filter to see your sites.</EmptyDescription></EmptyHeader><Button variant="outline" onClick={() => { onSearchChange(''); onOrgChange('') }}>Clear filters</Button></Empty>
              : view === 'cards' ? <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">{filtered.map((site) => <SiteCard key={site.id} site={site} multipleOrganizations={multipleOrganizations} onDelete={setPendingDelete} />)}</div>
                : <SitesTable sites={filtered} multipleOrganizations={multipleOrganizations} onDelete={setPendingDelete} />}
      <Dialog open={Boolean(pendingDelete)} onOpenChange={(open) => { if (!open) setPendingDelete(undefined) }}><DialogContent role="alertdialog"><DialogHeader><DialogTitle>Delete {pendingDelete?.name}?</DialogTitle><DialogDescription>The site stops collecting data now. You can restore it for 7 days.</DialogDescription></DialogHeader><DialogFooter><DialogClose asChild><Button variant="outline">Cancel</Button></DialogClose><Button variant="destructive" disabled={deletion.isPending} onClick={async () => { if (!pendingDelete) return; await deletion.mutateAsync(pendingDelete); setPendingDelete(undefined) }}>Delete site</Button></DialogFooter></DialogContent></Dialog>
    </PageContainer>
  )
}
