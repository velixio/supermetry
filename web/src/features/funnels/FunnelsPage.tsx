import { useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

import { useCreateFunnel, useFunnelResult, useFunnels, useUpdateFunnel } from './api'
import { FunnelBuilder } from './FunnelBuilder'
import { FunnelChart } from './FunnelChart'
import type { FunnelCountBy, FunnelStep, FunnelWindow } from './types'

type FunnelsPageProps = {
  siteId: number
  dateRange: { from: string; to: string }
  funnelId?: number
}

export function FunnelsPage({ siteId, dateRange, funnelId }: FunnelsPageProps) {
  const funnels = useFunnels(siteId)
  const create = useCreateFunnel(siteId)
  const update = useUpdateFunnel(siteId)
  const [name, setName] = useState('')
  const [selectedId, setSelectedId] = useState(funnelId)
  const selected = funnels.data?.find((funnel) => funnel.id === selectedId)
  const [previewSteps, setPreviewSteps] = useState<FunnelStep[]>([])
  const [countBy, setCountBy] = useState<FunnelCountBy>('sessions')
  const [window, setWindow] = useState<FunnelWindow>('session')
  const steps = previewSteps.length > 0 ? previewSteps : selected?.steps ?? []
  const result = useFunnelResult(siteId, dateRange, steps, countBy, window)

  useEffect(() => {
    if (selected) setName(selected.name)
  }, [selected])

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <header>
        <h1 className="text-xl font-semibold tracking-tight">Funnels</h1>
        <p className="text-sm text-muted-foreground">See where sessions or visitors continue and where they leave.</p>
      </header>
      <div className="grid min-w-0 gap-6 xl:grid-cols-[minmax(18rem,0.8fr)_minmax(0,1.5fr)]">
        <section className="flex flex-col gap-4 rounded-xl border bg-card p-4">
          <h2 className="font-medium">Saved funnels</h2>
          {funnels.data?.length ? (
            <ul className="flex flex-col gap-1">
              {funnels.data.map((funnel) => (
                <li key={funnel.id}>
                  <Button
                    className="w-full justify-start"
                    type="button"
                    variant={funnel.id === selected?.id ? 'secondary' : 'ghost'}
                    onClick={() => {
                      setSelectedId(funnel.id)
                      setPreviewSteps([])
                    }}
                  >
                    {funnel.name}
                  </Button>
                </li>
              ))}
            </ul>
          ) : <p className="text-sm text-muted-foreground">No saved funnels yet.</p>}
        </section>
        <section className="flex min-w-0 flex-col gap-5 rounded-xl border bg-card p-4">
          <Field>
            <FieldLabel htmlFor="funnel-count-by">Count by</FieldLabel>
            <Select value={countBy} onValueChange={(value) => setCountBy(value as FunnelCountBy)}>
              <SelectTrigger id="funnel-count-by" aria-label="Count by">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value="sessions">Sessions</SelectItem>
                  <SelectItem value="visitors">Visitors</SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
            <FieldDescription>
              Profile-linked visitors can continue across sessions. Anonymous visitors only link within one site-local day.
            </FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="funnel-window">Conversion window</FieldLabel>
            <Select value={window} onValueChange={(value) => setWindow(value as FunnelWindow)}>
              <SelectTrigger id="funnel-window" aria-label="Conversion window">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value="session">Same session</SelectItem>
                  <SelectItem value="1h">1 hour</SelectItem>
                  <SelectItem value="24h">24 hours</SelectItem>
                  <SelectItem value="7d">7 days</SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          </Field>
          <Field>
            <FieldLabel htmlFor="funnel-name">Funnel name</FieldLabel>
            <Input id="funnel-name" value={name} onChange={(event) => setName(event.target.value)} />
          </Field>
          <FunnelBuilder
            key={selected?.id ?? 'new'}
            initialSteps={selected?.steps}
            pending={create.isPending || update.isPending}
            onSubmit={async (nextSteps) => {
              setPreviewSteps(nextSteps)
              const input = { name: name.trim() || 'Untitled funnel', steps: nextSteps }
              if (selected) await update.mutateAsync({ id: selected.id, ...input })
              else await create.mutateAsync(input)
            }}
          />
          {result.data ? (
            <FunnelChart
              countLabel={result.data.count_by === 'visitors' ? 'Visitors' : 'Sessions'}
              steps={result.data.steps.map((step, index) => ({
                label: steps[index]?.value ?? `Step ${step.step}`,
                count: step.count,
              }))}
            />
          ) : null}
        </section>
      </div>
    </div>
  )
}
