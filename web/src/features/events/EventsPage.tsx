import { useEffect, useState, type FormEvent } from 'react'

import { ErrorState } from '@/components/ErrorState'
import { ListPager } from '@/components/ListPager'
import { ReplayLink } from '@/components/replay/ReplayLink'
import { BrowserLabel } from '@/components/visitor/BrowserLabel'
import { DeviceLabel } from '@/components/visitor/DeviceLabel'
import { Location } from '@/components/visitor/Location'
import { OsLabel } from '@/components/visitor/OsLabel'
import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import type { Filter } from '@/lib/search'

import { useEvents, useProperties, usePropertyValues } from './api'
import type { EventExploreItem } from './types'

type EventRow = EventExploreItem & Partial<{ city: string; os: string }>
type Paged<T> = T & Partial<{ total: number; total_capped: boolean }>

type EventsPageProps = {
  siteId: number
  orgId: number
  dateRange: { from: string; to: string }
  filters: Filter[]
  name: string
  propertyKey: string
  cursor?: string
  page: number
  onNameChange: (name: string) => void
  onPropertyChange: (key: string) => void
  onNext: (cursor: string) => void
  onPrevious: () => void
  onFirst: () => void
  org?: string
  site?: string
}

export function EventsPage({
  siteId, orgId, dateRange, filters, name, propertyKey, cursor, page,
  onNameChange, onPropertyChange, onFirst, onNext, onPrevious, org, site,
}: EventsPageProps) {
  const [draftName, setDraftName] = useState(name)
  useEffect(() => setDraftName(name), [name])
  const events = useEvents(siteId, orgId, dateRange, filters, name, cursor)
  const properties = useProperties(siteId, orgId, dateRange, name)
  const values = usePropertyValues(siteId, orgId, propertyKey, dateRange, name)
  const data = events.data as Paged<NonNullable<typeof events.data>> | undefined

  function submitName(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    onNameChange(draftName.trim())
  }

  return (
    <main className="flex min-w-0 flex-col gap-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-xl font-semibold tracking-tight">Events</h1>
        <p className="text-sm text-muted-foreground">Inspect recent pageviews, custom events, and their properties.</p>
      </header>
      <form className="flex items-end gap-2" onSubmit={submitName}>
        <FieldGroup className="max-w-sm flex-1">
          <Field>
            <FieldLabel htmlFor="event-name">Event name</FieldLabel>
            <Input id="event-name" placeholder="All events" value={draftName} onChange={(event) => setDraftName(event.target.value)} />
          </Field>
        </FieldGroup>
        <Button type="submit" variant="outline">Apply event name</Button>
      </form>
      <section className="flex flex-col gap-3" aria-labelledby="properties-heading">
        <h2 id="properties-heading" className="font-medium">Properties</h2>
        <div className="flex flex-wrap gap-2">
          {(properties.data ?? []).map((property) => (
            <Button
              key={property.key}
              aria-label={`Property ${property.key}, ${property.events} events`}
              aria-pressed={propertyKey === property.key}
              size="sm"
              type="button"
              variant={propertyKey === property.key ? 'secondary' : 'outline'}
              onClick={() => onPropertyChange(propertyKey === property.key ? '' : property.key)}
            >
              {property.key} <span className="text-muted-foreground">{property.events}</span>
            </Button>
          ))}
        </div>
        {propertyKey ? (
          <div className="rounded-xl border bg-card p-4">
            <h3 className="mb-3 text-sm font-medium">Top values for {propertyKey}</h3>
            <div className="flex flex-col gap-2">
              {(values.data ?? []).map((item) => (
                <div className="flex justify-between gap-4 text-sm" key={item.value}>
                  <span>{item.value}</span><span className="text-muted-foreground">{item.events} events</span>
                </div>
              ))}
            </div>
          </div>
        ) : null}
      </section>
      {events.isError ? (
        <ErrorState detail={events.error.message} onRetry={() => void events.refetch()} />
      ) : events.isPending ? (
        <div aria-label="Loading events" className="flex flex-col gap-3">
          <Skeleton className="h-10 w-full" /><Skeleton className="h-64 w-full" />
        </div>
      ) : events.data.data.length === 0 ? (
        <Empty><EmptyHeader><EmptyTitle>No events found</EmptyTitle><EmptyDescription>Try another date range, event name, or filter.</EmptyDescription></EmptyHeader></Empty>
      ) : (
        <div className="overflow-x-auto rounded-xl border bg-card">
          <Table aria-label="Events">
            <TableHeader><TableRow>
              <TableHead>Time</TableHead><TableHead>Kind</TableHead><TableHead>Name</TableHead><TableHead>Page</TableHead>
              <TableHead>Replay</TableHead>
              <TableHead>Properties</TableHead><TableHead>Location</TableHead><TableHead>Browser</TableHead><TableHead>OS</TableHead><TableHead>Device</TableHead><TableHead>Source</TableHead>
            </TableRow></TableHeader>
            <TableBody>{(events.data.data as EventRow[]).map((event, index) => (
              <TableRow key={`${event.ts}:${event.name}:${event.page}:${index}`}>
                <TableCell className="whitespace-nowrap">{new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(event.ts))}</TableCell>
                <TableCell>{event.kind}</TableCell><TableCell>{event.name}</TableCell><TableCell>{event.page || '—'}</TableCell>
                <TableCell>{org && site ? <ReplayLink hasReplay={(event as { has_replay?: boolean }).has_replay} org={org} site={site} /> : null}</TableCell>
                <TableCell>{event.props ? Object.entries(event.props).map(([key, value]) => <span className="mr-2 whitespace-nowrap" key={key}>{key}: {value}</span>) : '—'}</TableCell>
                <TableCell><Location city={event.city} country={event.country} /></TableCell>
                <TableCell><BrowserLabel value={event.browser} /></TableCell>
                <TableCell><OsLabel value={event.os} /></TableCell>
                <TableCell><DeviceLabel value={event.device} /></TableCell>
                <TableCell>{event.source || '—'}</TableCell>
              </TableRow>
            ))}</TableBody>
          </Table>
        </div>
      )}
      {!events.isPending && !events.isError ? (
        <ListPager
          fetching={events.isFetching}
          hasNext={Boolean(data?.next_cursor)}
          page={page}
          pageSize={50}
          rowCount={data?.data.length ?? 0}
          total={data?.total}
          totalCapped={data?.total_capped}
          onFirst={onFirst}
          onNext={() => data?.next_cursor && onNext(data.next_cursor)}
          onPrevious={onPrevious}
        />
      ) : null}
    </main>
  )
}
