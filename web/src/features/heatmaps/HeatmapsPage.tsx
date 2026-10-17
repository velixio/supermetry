import { useEffect, useRef, useState } from 'react'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldLabel } from '@/components/ui/field'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useStats } from '@/lib/stats'

import { useHeatmapReport, useHeatmapSite } from './api'
import type { HeatmapCell, HeatmapDevice, ScrollBand } from './types'
import { previewURL } from './previewURL'

const frameWidths: Record<HeatmapDevice, number> = {
  mobile: 390,
  tablet: 820,
  desktop: 1280,
}

type HeatmapsPageProps = {
  siteId: number
  orgId: number
  dateRange: { from: string; to: string }
  device: HeatmapDevice
  path: string
  onChange: (value: { device: HeatmapDevice; path: string }) => void
}

function pageHeight(cells: HeatmapCell[]): number {
  let highestCell = 0
  for (const cell of cells) highestCell = Math.max(highestCell, cell.gy)
  return Math.max(640, (highestCell + 1) * 20)
}

function ClickCanvas({ cells, height, width }: { cells: HeatmapCell[]; height: number; width: number }) {
  const canvasRef = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    const canvas = canvasRef.current
    const context = canvas?.getContext('2d')
    if (!canvas || !context) return
    const renderWidth = Math.min(width, 640)
    const renderHeight = Math.min(height, 8_192)
    canvas.width = renderWidth
    canvas.height = renderHeight
    context.clearRect(0, 0, renderWidth, renderHeight)
    context.setTransform(renderWidth / width, 0, 0, renderHeight / height, 0, 0)
    let maximum = 0
    for (const cell of cells) maximum = Math.max(maximum, cell.clicks)
    if (maximum === 0) return

    for (const cell of cells) {
      const strength = cell.clicks / maximum
      const x = ((cell.gx + 0.5) / 100) * width
      const y = cell.gy * 20 + 10
      const radius = 22 + strength * 26
      const gradient = context.createRadialGradient(x, y, 0, x, y, radius)
      gradient.addColorStop(0, `rgba(244, 63, 94, ${0.55 + strength * 0.35})`)
      gradient.addColorStop(0.45, `rgba(249, 115, 22, ${0.3 + strength * 0.3})`)
      gradient.addColorStop(1, 'rgba(250, 204, 21, 0)')
      context.fillStyle = gradient
      context.beginPath()
      context.arc(x, y, radius, 0, Math.PI * 2)
      context.fill()
    }
  }, [cells, height, width])

  return (
    <canvas
      ref={canvasRef}
      aria-label="Click density overlay"
      className="pointer-events-none absolute inset-0 h-full w-full blur-[1px]"
    />
  )
}

function ScrollOverlay({ bands }: { bands: ScrollBand[] }) {
  return (
    <div aria-label="Scroll reach overlay" className="pointer-events-none absolute inset-0">
      {bands.map((item) => {
        const percentage = Math.round(item.share * 100)
        return (
          <div
            key={item.band}
            className="absolute inset-x-0 border-t border-background/60 bg-accent/10"
            style={{ top: `${Math.max(0, item.band - 10)}%`, height: '10%' }}
          >
            <span className="absolute right-2 top-1 rounded-md bg-background/90 px-2 py-1 text-xs font-medium shadow-sm">
              {item.band}% depth · {percentage}% reached
            </span>
          </div>
        )
      })}
      <div className="absolute inset-0 bg-gradient-to-b from-transparent via-accent/10 to-accent/40" />
    </div>
  )
}

function Preview({
  cells,
  device,
  host,
  path,
  scrollBands,
  showScroll,
}: {
  cells: HeatmapCell[]
  device: HeatmapDevice
  host: string
  path: string
  scrollBands: ScrollBand[]
  showScroll: boolean
}) {
  const source = previewURL(host, path)
  const [frameState, setFrameState] = useState<'loading' | 'loaded' | 'blocked'>('loading')
  const fallbackTimer = useRef<number | undefined>(undefined)
  const height = pageHeight(cells)
  const width = frameWidths[device]

  useEffect(() => {
    setFrameState('loading')
    fallbackTimer.current = window.setTimeout(() => setFrameState('blocked'), 4_000)
    return () => window.clearTimeout(fallbackTimer.current)
  }, [source])

  const finishLoading = (state: 'loaded' | 'blocked') => {
    window.clearTimeout(fallbackTimer.current)
    setFrameState(state)
  }

  return (
    <div className="overflow-x-auto rounded-xl border bg-muted/30 p-3">
      <div className="mx-auto" style={{ width: `${width}px`, maxWidth: '100%' }}>
        {frameState === 'blocked' || source === '' ? (
          <div className="mb-3 rounded-lg border bg-card p-4 text-sm">
            <p className="font-medium">Page preview blocked</p>
            <p className="mt-1 text-muted-foreground">
              The site may block embedding with X-Frame-Options or CSP. Allow Supermetry&apos;s host in the site&apos;s frame-ancestors directive to show the live page.
            </p>
          </div>
        ) : null}
        <div
          aria-label={frameState === 'blocked' || source === '' ? 'Page outline' : undefined}
          className="relative overflow-hidden rounded-lg bg-background"
          style={{ height: `${height}px` }}
        >
          {frameState !== 'blocked' && source !== '' ? (
            <iframe
              className="absolute inset-0 h-full border-0 bg-background"
              sandbox="allow-same-origin allow-scripts"
              src={source}
              style={{ width: `${width}px` }}
              title={`Live preview of ${path}`}
              onError={() => finishLoading('blocked')}
              onLoad={() => finishLoading('loaded')}
            />
          ) : (
            <div
              className="absolute inset-0 opacity-40"
              style={{
                backgroundImage:
                  'linear-gradient(to right, var(--border) 1px, transparent 1px), linear-gradient(to bottom, var(--border) 1px, transparent 1px)',
                backgroundSize: '40px 40px',
              }}
            />
          )}
          <ClickCanvas cells={cells} height={height} width={width} />
          {showScroll ? <ScrollOverlay bands={scrollBands} /> : null}
        </div>
      </div>
    </div>
  )
}

export function HeatmapsPage({ siteId, orgId, dateRange, device, path, onChange }: HeatmapsPageProps) {
  const [showScroll, setShowScroll] = useState(false)
  const site = useHeatmapSite(siteId, orgId)
  const pages = useStats({
    siteId,
    dateRange,
    filters: [],
    metrics: ['pageviews'],
    dimensions: ['page'],
    orderBy: [{ column: 'pageviews', direction: 'desc' }],
    limit: 50,
  })
  const topPages = (pages.data?.rows ?? [])
    .map((row) => String(row.page ?? ''))
    .filter((page) => page !== '')
  const selectedPath = path || topPages[0] || ''
  const pageOptions = selectedPath !== '' && !topPages.includes(selectedPath)
    ? [selectedPath, ...topPages]
    : topPages
  const enabled = site.data?.heatmaps_enabled === true
  const report = useHeatmapReport(siteId, dateRange, selectedPath, device, enabled)

  if (site.isPending) {
    return <Skeleton aria-label="Loading heatmaps" className="h-80 w-full" />
  }

  if (site.isError) {
    return <ErrorState level="page" detail={site.error?.detail} onRetry={() => void site.refetch()} />
  }

  if (!enabled) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>Heatmaps are not collecting yet</EmptyTitle>
          <EmptyDescription>
            Enable heatmaps in site settings and install the full tracker script to start collecting private, aggregated clicks.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  const cells = report.data?.cells ?? []
  const scrollBands = report.data?.scroll_bands ?? []
  const totalClicks = report.data?.totals.clicks ?? 0
  const pageviews = report.data?.totals.pageviews_sampled ?? 0
  const host = site.data?.allowed_hosts[0] ?? ''

  return (
    <PageContainer>
      <PageHeader
        title="Heatmaps"
        description="See aggregated clicks and how far visitors scroll, without recording sessions."
        actions={
          <Button
            type="button"
            variant={showScroll ? 'secondary' : 'outline'}
            aria-pressed={showScroll}
            onClick={() => setShowScroll((current) => !current)}
          >
            Scroll map
          </Button>
        }
      />
      <Card>
        <CardHeader>
          <CardTitle>Page preview</CardTitle>
          <CardDescription>Choose a popular page and viewport.</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap items-end gap-4">
          <Field className="min-w-56 flex-1">
            <FieldLabel htmlFor="heatmap-page">Page</FieldLabel>
            <select
              id="heatmap-page"
              className="h-9 rounded-md border bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
              value={selectedPath}
              onChange={(event) => onChange({ device, path: event.target.value })}
            >
              {pageOptions.map((page) => <option key={page} value={page}>{page}</option>)}
            </select>
          </Field>
          <Tabs value={device}>
            <TabsList aria-label="Device">
              <TabsTrigger value="mobile" onClick={() => onChange({ device: 'mobile', path: selectedPath })}>Mobile</TabsTrigger>
              <TabsTrigger value="tablet" onClick={() => onChange({ device: 'tablet', path: selectedPath })}>Tablet</TabsTrigger>
              <TabsTrigger value="desktop" onClick={() => onChange({ device: 'desktop', path: selectedPath })}>Desktop</TabsTrigger>
            </TabsList>
          </Tabs>
          <p className="text-sm tabular-nums text-muted-foreground">{totalClicks} clicks · {pageviews} pageviews sampled</p>
        </CardContent>
      </Card>
      {report.isError ? (
        <ErrorState level="page" detail={report.error?.detail} onRetry={() => void report.refetch()} />
      ) : report.isPending || report.isLoading ? (
        <Skeleton aria-label="Loading heatmap report" className="h-96 w-full" />
      ) : totalClicks === 0 && pageviews === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>No heatmap data for this selection</EmptyTitle>
            <EmptyDescription>Try another page, device, or date range.</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <Preview
          cells={cells}
          device={device}
          host={host}
          path={selectedPath}
          scrollBands={scrollBands}
          showScroll={showScroll}
        />
      )}
    </PageContainer>
  )
}
