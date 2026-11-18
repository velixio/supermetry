import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useEffect, useState } from 'react'

import { LoaderCircleIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { cn } from '@/lib/utils'
import { deleteSite, getSite } from '@/features/sites/api'
import { heartbeatDemo } from './api'

type DemoSiteSummary = {
  id: number
  name: string
  public_id?: string
  is_demo?: boolean
  demo_ready?: boolean
  demo_failed?: boolean
  demo_expires_at?: string
}

export function demoSiteRefetchInterval(query: {
  state: { data?: { demo_ready?: boolean; demo_failed?: boolean } }
}): 2000 | false {
  return query.state.data?.demo_ready === false && query.state.data.demo_failed !== true ? 2_000 : false
}

export function DemoBanner({
  org,
  site,
}: {
  org: { id: number; slug: string }
  site: DemoSiteSummary
}) {
  useDemoHeartbeat(site.id)
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const current = useQuery({
    queryKey: ['site', site.id],
    queryFn: () => getSite(site.id, org.id),
    initialData: site,
    refetchInterval: demoSiteRefetchInterval,
    retry: (count, error) => !isNotFound(error) && count < 1,
  })
  useEffect(() => {
    if (!site.demo_expires_at) return
    const expiresAt = new Date(site.demo_expires_at).getTime()
    if (!Number.isFinite(expiresAt)) return
    let interval: ReturnType<typeof setInterval> | undefined
    const refetch = () => {
      void queryClient.invalidateQueries({ queryKey: ['site', site.id], exact: true })
    }
    const start = () => {
      refetch()
      interval = setInterval(refetch, 60_000)
    }
    const timeout = setTimeout(start, Math.max(0, expiresAt - Date.now()))
    return () => {
      clearTimeout(timeout)
      if (interval !== undefined) clearInterval(interval)
    }
  }, [queryClient, site.demo_expires_at, site.id])
  useEffect(() => {
    if (!isNotFound(current.error)) return
    void queryClient.invalidateQueries({ queryKey: ['me'] })
    queryClient.removeQueries({ queryKey: ['site', site.id] })
    void navigate({ to: '/app/$org', params: { org: org.slug } })
  }, [current.error, navigate, org.slug, queryClient, site.id])
  const remove = useMutation({
    mutationFn: () => deleteSite(site.id, org.id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['me'] })
      queryClient.removeQueries({ queryKey: ['site', site.id] })
    },
  })
  const currentSite = current.data as DemoSiteSummary
  const failed = currentSite.demo_failed === true
  const ready = currentSite.demo_ready !== false
  const title = failed ? 'Demo data could not be generated.' : ready ? "You're viewing demo data" : 'Generating demo data…'
  const description = failed
    ? 'Remove this demo project and try creating it again.'
    : ready
      ? 'Everything here is synthetic and safe to explore.'
      : 'The dashboard will fill in as the project is prepared.'
  const expiry = useDemoExpiry(currentSite.demo_expires_at)
  const expiryShort = expiry?.replace('Demo expires in ', '')

  const [open, setOpen] = useState(false)
  const [confirming, setConfirming] = useState(false)
  // A compact chip in the app header: the page keeps its full height for data. The chip names the state
  // (and the remaining lifetime); the explanation and the remove action are one click away.
  const chipLabel = failed ? 'Demo failed' : ready ? (expiryShort ? `Demo · ${expiryShort}` : 'Demo') : 'Generating demo…'

  return (
    <>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button
            type="button"
            size="sm"
            variant="outline"
            aria-label={`${title} ${chipLabel === 'Demo' ? '' : `(${chipLabel})`}`.trim()}
            className={cn(
              'h-7 gap-1.5 rounded-full px-2.5 text-xs font-medium shadow-none',
              failed ? 'border-destructive/40 text-destructive' : 'border-chart-1/40 text-foreground',
            )}
            data-testid="demo-chip"
          >
            {ready || failed
              ? <span aria-hidden="true" className={cn('size-1.5 rounded-full', failed ? 'bg-destructive' : 'bg-chart-1')} />
              : <LoaderCircleIcon aria-hidden="true" className="size-3 animate-spin motion-reduce:animate-none" />}
            <span aria-hidden="true" className="tabular-nums">{chipLabel}</span>
          </Button>
        </PopoverTrigger>
        <PopoverContent align="end" className="w-72 space-y-2 p-3 text-sm">
          <p className="font-medium">{title}</p>
          <p className="text-muted-foreground">{description}</p>
          {expiry ? <p className="text-xs text-muted-foreground tabular-nums">{expiry}</p> : null}
          <div className="flex justify-end pt-1">
            <Button type="button" size="sm" variant="ghost" className="h-7 px-2 text-muted-foreground" onClick={() => { setOpen(false); setConfirming(true) }}>
              Remove demo project
            </Button>
          </div>
          {remove.isError ? <p role="alert" className="text-destructive">The demo project could not be removed.</p> : null}
        </PopoverContent>
      </Popover>
      <Dialog open={confirming} onOpenChange={setConfirming}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Remove demo project?</DialogTitle>
            <DialogDescription>This deletes the demo project and all of its synthetic data.</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <DialogClose asChild><Button type="button" variant="outline">Cancel</Button></DialogClose>
            <Button type="button" variant="destructive" disabled={remove.isPending} onClick={() => remove.mutate()}>
              {remove.isPending ? 'Removing…' : 'Remove demo'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      {remove.isError && !open ? <span role="alert" className="sr-only">The demo project could not be removed.</span> : null}
    </>
  )
}

function isNotFound(error: unknown): boolean {
  return typeof error === 'object' && error !== null && 'status' in error && error.status === 404
}

function useDemoExpiry(expiresAt?: string): string | undefined {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!expiresAt) return
    const timer = setInterval(() => setNow(Date.now()), 60_000)
    return () => clearInterval(timer)
  }, [expiresAt])
  if (!expiresAt) return undefined
  const remainingMinutes = Math.max(0, Math.ceil((new Date(expiresAt).getTime() - now) / 60_000))
  if (remainingMinutes < 60) return `Demo expires in ${remainingMinutes}m`
  return `Demo expires in ${Math.floor(remainingMinutes / 60)}h ${remainingMinutes % 60}m`
}

function useDemoHeartbeat(siteId: number) {
  useEffect(() => {
    let timer: ReturnType<typeof setInterval> | undefined
    let stopped = false
    const stop = () => {
      if (timer !== undefined) clearInterval(timer)
      timer = undefined
    }
    const send = () => {
      void heartbeatDemo(siteId).catch((error: unknown) => {
        if (isNotFound(error)) {
          stopped = true
          stop()
        }
      })
    }
    const sync = () => {
      stop()
      if (stopped || document.visibilityState !== 'visible') return
      send()
      timer = setInterval(send, 30_000)
    }

    sync()
    document.addEventListener('visibilitychange', sync)
    return () => {
      stop()
      document.removeEventListener('visibilitychange', sync)
    }
  }, [siteId])
}
