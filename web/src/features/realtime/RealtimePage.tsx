import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Bar, BarChart, ResponsiveContainer, Tooltip, XAxis } from 'recharts'
import {
  ActivityIcon,
  LaptopIcon,
  MonitorIcon,
  MousePointerClickIcon,
  SmartphoneIcon,
  TabletIcon,
} from 'lucide-react'

import { ErrorState } from '@/components/ErrorState'
import { Flag } from '@/components/Flag'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { countryName } from '@/lib/country'
import { fmt } from '@/lib/format'

import { useRealtimePage } from './useRealtimePage'

type RealtimePageProps = {
  siteId: number
  dateRange: { from: string; to: string }
  timezone: string
}

type RankedItem = { label: string; value: number; countryCode?: string }

function RankedList({ items, empty = 'No activity yet.' }: { items: RankedItem[]; empty?: string }) {
  if (items.length === 0) return <p className="text-sm text-muted-foreground">{empty}</p>
  const maximum = Math.max(...items.map((item) => item.value), 1)
  return (
    <ol className="flex flex-col gap-1">
      {items.slice(0, 10).map((item, index) => (
        <li className="relative grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 rounded-md px-2 py-1.5 text-sm" key={`${item.label}:${index}`}>
          <span aria-hidden="true" className="absolute inset-y-1 left-0 rounded-sm bg-accent/10" style={{ width: `${Math.max(2, (item.value / maximum) * 100)}%` }} />
          <span className="relative flex min-w-0 items-center gap-2 truncate" title={item.label}>
            {item.countryCode ? <Flag code={item.countryCode} /> : null}
            <span className="truncate">{item.label}</span>
          </span>
          <span className="relative tabular-nums text-muted-foreground">{fmt.number(item.value)}</span>
        </li>
      ))}
    </ol>
  )
}

function RankedPanel({ title, children }: { title: string; children: ReactNode }) {
  return <section className="min-w-0"><h3 className="mb-2 text-sm font-medium">{title}</h3>{children}</section>
}

function relativeTime(timestamp: string, now: number): string {
  const elapsed = Math.max(0, Math.floor((now - new Date(timestamp).getTime()) / 1_000))
  if (elapsed < 60) return `${elapsed}s ago`
  const minutes = Math.floor(elapsed / 60)
  if (minutes < 60) return `${minutes}m ago`
  return `${Math.floor(minutes / 60)}h ago`
}

function DeviceIcon({ device }: { device: string }) {
  const normalized = device.toLowerCase()
  if (normalized.includes('mobile') || normalized.includes('phone')) return <SmartphoneIcon aria-hidden="true" />
  if (normalized.includes('tablet')) return <TabletIcon aria-hidden="true" />
  if (normalized.includes('desktop')) return <MonitorIcon aria-hidden="true" />
  return <LaptopIcon aria-hidden="true" />
}

type ActivityItem = NonNullable<ReturnType<typeof useRealtimePage>['data']>['recent_activity'][number]

function ActivityFeed({ items }: { items: ActivityItem[] }) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1_000)
    return () => window.clearInterval(timer)
  }, [])
  const activity = useMemo(
    () => [...items].sort(
      (left, right) => new Date(right.timestamp).getTime() - new Date(left.timestamp).getTime(),
    ),
    [items],
  )

  return (
    <section aria-labelledby="recent-activity-title" className="border-y py-4">
      <header className="mb-3 flex items-center justify-between gap-3">
        <div>
          <h2 id="recent-activity-title" className="font-medium">Recent activity</h2>
          <p className="text-sm text-muted-foreground">Newest pageviews and events from active visitors.</p>
        </div>
        <ActivityIcon aria-hidden="true" className="text-muted-foreground" />
      </header>
      {activity.length === 0 ? (
        <p className="py-8 text-center text-sm text-muted-foreground">Waiting for the next pageview or event.</p>
      ) : (
        <ol className="divide-y">
          {activity.map((item, index) => (
            <li aria-label="Recent activity item" className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 py-3" key={`${item.timestamp}:${item.kind}:${index}`}>
              <span className="flex items-center gap-2 text-muted-foreground">
                <Flag code={item.country_code} />
                <span className="hidden sm:inline">{item.city || 'Unknown'}</span>
                <span className="[&_svg]:size-4"><DeviceIcon device={item.device} /></span>
              </span>
              <span className="min-w-0">
                <span className="flex min-w-0 items-center gap-2">
                  {item.kind === 'custom' ? <MousePointerClickIcon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" /> : null}
                  <span className="truncate text-sm font-medium">{item.kind === 'custom' ? item.event_name : item.page}</span>
                </span>
                <span className="block truncate text-xs text-muted-foreground">{item.browser} on {item.device} · {item.source || 'Direct'}{item.kind === 'custom' && item.page ? ` · ${item.page}` : ''}</span>
              </span>
              <time className="text-xs tabular-nums text-muted-foreground" dateTime={item.timestamp}>{relativeTime(item.timestamp, now)}</time>
            </li>
          ))}
        </ol>
      )}
    </section>
  )
}

function BreakdownCard({ title, description, className, children }: { title: string; description?: string; className?: string; children: ReactNode }) {
  return (
    <Card className={className}>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description ? <CardDescription>{description}</CardDescription> : null}
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  )
}

export function RealtimePage({ siteId, timezone }: RealtimePageProps) {
  const realtime = useRealtimePage(siteId)

  if (realtime.isPending) {
    return (
      <div aria-label="Loading realtime analytics" className="flex flex-col gap-4">
        <Skeleton className="h-8 w-40" />
        <div className="grid gap-4 lg:grid-cols-[minmax(14rem,0.7fr)_minmax(0,2fr)]">
          <Skeleton className="h-64 w-full" />
          <Skeleton className="h-64 w-full" />
        </div>
        <Skeleton className="h-80 w-full" />
      </div>
    )
  }

  if (realtime.isError || !realtime.data) {
    return <ErrorState detail={realtime.error?.detail} onRetry={() => void realtime.refetch()} />
  }

  const data = realtime.data
  const formatter = new Intl.DateTimeFormat(undefined, {
    timeZone: timezone,
    hour: 'numeric',
    minute: '2-digit',
  })
  const chartPoints = data.visitors_per_minute.slice(-30).map((point) => ({
    ...point,
    label: formatter.format(new Date(point.minute)),
  }))

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-xl font-semibold tracking-tight">Realtime</h1>
        <p className="text-sm text-muted-foreground">A live view of the last 30 minutes. Updates every 5 seconds.</p>
      </header>

      <div className="grid min-w-0 gap-4 lg:grid-cols-[minmax(14rem,0.7fr)_minmax(0,2fr)]">
        <Card className="justify-between overflow-hidden border-accent/30 bg-accent/[0.025]">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <span className="relative flex size-2" aria-hidden="true">
                <span className="absolute inline-flex size-full rounded-full bg-accent opacity-50 motion-safe:animate-ping" />
                <span className="relative inline-flex size-2 rounded-full bg-accent" />
              </span>
              Live now
            </CardTitle>
            <CardDescription>Active in the last 5 minutes</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-2 pb-2">
            <output aria-live="polite" className="text-6xl font-semibold tracking-tight tabular-nums">{fmt.number(data.active_visitors)}</output>
            <p className="text-sm text-muted-foreground">
              {fmt.number(data.visitors_last_30m)} visitors and {fmt.number(data.pageviews_last_30m)} pageviews in the last 30 minutes
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Visitors per minute</CardTitle>
            <CardDescription>The pulse of the site, newest minute on the right.</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="h-44 w-full" role="img" aria-label="Visitors during the last 30 minutes">
              <ResponsiveContainer width="100%" height="100%" minWidth={0}>
                <BarChart data={chartPoints} accessibilityLayer>
                  <XAxis dataKey="label" axisLine={false} tickLine={false} interval="preserveStartEnd" minTickGap={30} />
                  <Tooltip />
                  <Bar dataKey="visitors" fill="var(--chart-1)" radius={[3, 3, 0, 0]} />
                </BarChart>
              </ResponsiveContainer>
            </div>
          </CardContent>
        </Card>
      </div>

      <div className="grid min-w-0 gap-4 md:grid-cols-2 xl:grid-cols-3">
        <BreakdownCard className="xl:col-span-2" title="Top pages"><RankedList items={data.top_pages.map((item) => ({ label: item.page, value: item.visitors }))} /></BreakdownCard>
        <BreakdownCard title="Entry pages"><RankedList items={data.top_entry_pages.map((item) => ({ label: item.page, value: item.visitors }))} /></BreakdownCard>
        <BreakdownCard title="Sources"><RankedList items={data.top_sources.map((item) => ({ label: item.source, value: item.visitors }))} /></BreakdownCard>
        <BreakdownCard title="Countries"><RankedList items={data.countries.map((item) => ({ label: countryName(item.country_code), countryCode: item.country_code, value: item.visitors }))} /></BreakdownCard>
        <BreakdownCard title="Locations">
          <div className="grid gap-5 sm:grid-cols-2">
            <RankedPanel title="Regions"><RankedList items={data.regions.map((item) => ({ label: item.label, countryCode: item.country_code, value: item.visitors }))} /></RankedPanel>
            <RankedPanel title="Cities"><RankedList items={data.cities.map((item) => ({ label: item.label, countryCode: item.country_code, value: item.visitors }))} /></RankedPanel>
          </div>
        </BreakdownCard>
        <BreakdownCard className="xl:col-span-2" title="Technology">
          <Tabs defaultValue="devices">
            <TabsList>
              <TabsTrigger value="devices">Devices</TabsTrigger>
              <TabsTrigger value="browsers">Browsers</TabsTrigger>
              <TabsTrigger value="operating-systems">Operating systems</TabsTrigger>
            </TabsList>
            <TabsContent value="devices"><RankedList items={data.devices.map((item) => ({ label: item.label, value: item.visitors }))} /></TabsContent>
            <TabsContent value="browsers"><RankedList items={data.browsers.map((item) => ({ label: item.label, value: item.visitors }))} /></TabsContent>
            <TabsContent value="operating-systems"><RankedList items={data.operating_systems.map((item) => ({ label: item.label, value: item.visitors }))} /></TabsContent>
          </Tabs>
        </BreakdownCard>
        <BreakdownCard title="Events & goals"><RankedList items={data.events.map((item) => ({ label: item.name, value: item.count }))} empty="No events or goals yet." /></BreakdownCard>
      </div>

      <ActivityFeed items={data.recent_activity} />
    </div>
  )
}
