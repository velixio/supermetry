import { useMemo, useState, type FormEvent } from 'react'
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog'
import { Field, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import type { Site } from '@/lib/me'
import { resolveRange } from '@/lib/search'
import { en } from '@/messages/en'

import { useCreatePixel, usePixels, usePixelStats } from './api'
import { formatViews } from './format'
import { PixelList } from './PixelList'

export { formatViews } from './format'

function CreatePixelDialog({ siteId }: { siteId: number }) {
  const create = useCreatePixel(siteId)
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [label, setLabel] = useState('views')
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    await create.mutateAsync({ name: name.trim(), badge_label: label.trim() })
    setName('')
    setLabel('views')
    setOpen(false)
  }
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild><Button>Create pixel</Button></DialogTrigger>
      <DialogContent>
        <DialogHeader><DialogTitle>Create pixel</DialogTitle><DialogDescription>Add one badge or invisible image for each place you want to measure.</DialogDescription></DialogHeader>
        <form className="flex flex-col gap-4" onSubmit={(event) => void submit(event)}>
          <FieldGroup>
            <Field><FieldLabel htmlFor="pixel-name">Name</FieldLabel><Input id="pixel-name" maxLength={120} required value={name} onChange={(event) => setName(event.target.value)} /></Field>
            <Field><FieldLabel htmlFor="pixel-label">Badge label</FieldLabel><Input id="pixel-label" maxLength={40} required value={label} onChange={(event) => setLabel(event.target.value)} /></Field>
          </FieldGroup>
          {create.error ? <FieldError>{create.error.message}</FieldError> : null}
          <Button type="submit" disabled={create.isPending || !name.trim() || !label.trim()}>{create.isPending ? 'Creating…' : 'Create pixel'}</Button>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function PixelDashboard({ site, homepage }: { site: Site; homepage: string }) {
  const [pixel, setPixel] = useState(0)
  const range = useMemo(() => resolveRange('30d', site.timezone ?? 'UTC', new Date()), [site.timezone])
  const pixels = usePixels(site.id)
  const stats = usePixelStats(site.id, { ...range, pixel: pixel || undefined })
  const daily = stats.data?.daily ?? []
  return (
    <PageContainer>
      <PageHeader title={site.name} description="Pixel analytics for badges and images." actions={<CreatePixelDialog siteId={site.id} />} />
      <Card>
        <CardHeader className="flex-row items-start justify-between gap-4">
          <div><CardTitle>Views</CardTitle><CardDescription>{en.pixels.viewsNote}</CardDescription></div>
          <Select value={String(pixel)} onValueChange={(value) => setPixel(Number(value))}>
            <SelectTrigger aria-label="Pixel filter"><SelectValue /></SelectTrigger>
            <SelectContent><SelectItem value="0">All pixels</SelectItem>{pixels.data?.map((item) => <SelectItem key={item.id} value={String(item.id)}>{item.name}</SelectItem>)}</SelectContent>
          </Select>
        </CardHeader>
        <CardContent>
          {stats.isPending ? <p className="text-sm text-muted-foreground" role="status">Loading views…</p> : null}
          {stats.isError ? <p className="text-sm text-destructive" role="alert">{stats.error.message}</p> : null}
          {stats.data && daily.length === 0 ? <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">No views in this period yet.</p> : null}
          {daily.length > 0 ? (
            <div className="h-64" role="img" aria-label="Daily proxy and direct views">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart accessibilityLayer data={daily} margin={{ left: 0, right: 8 }}>
                  <CartesianGrid vertical={false} strokeDasharray="3 3" />
                  <XAxis dataKey="day" tickLine={false} axisLine={false} minTickGap={24} />
                  <YAxis allowDecimals={false} tickLine={false} axisLine={false} width={36} />
                  <Tooltip />
                  <Area dataKey="direct" name="Direct" stackId="views" fill="var(--chart-2)" stroke="var(--chart-2)" />
                  <Area dataKey="proxy" name="Image proxy" stackId="views" fill="var(--chart-1)" stroke="var(--chart-1)" />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          ) : null}
        </CardContent>
      </Card>
      {pixels.isError ? <p className="text-sm text-destructive" role="alert">{pixels.error.message}</p> : null}
      {pixels.data ? <PixelList siteId={site.id} pixels={pixels.data} homepage={homepage} /> : null}
      <Card>
        <CardHeader><CardTitle>Countries</CardTitle><CardDescription>{en.pixels.noCountryNote}</CardDescription></CardHeader>
        <CardContent>
          {stats.data?.countries.length ? <ol className="flex flex-col gap-2">{stats.data.countries.slice(0, 10).map((country) => <li className="flex justify-between border-b pb-2 last:border-0" key={country.country}><span>{country.country}</span><span className="tabular-nums">{formatViews(country.views)}</span></li>)}</ol> : <p className="text-sm text-muted-foreground">No direct-view country data in this period.</p>}
        </CardContent>
      </Card>
    </PageContainer>
  )
}
