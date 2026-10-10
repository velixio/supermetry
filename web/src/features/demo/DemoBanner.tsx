import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useEffect, useState } from 'react'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
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

  return (
    <Alert className="mb-4">
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-col gap-1">
          <span>{description}</span>
          {expiry ? <span>{expiry}</span> : null}
        </div>
        <Dialog>
          <DialogTrigger asChild>
            <Button type="button" size="sm" variant="outline">Remove demo project</Button>
          </DialogTrigger>
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
        {remove.isError ? <span role="alert">The demo project could not be removed.</span> : null}
      </AlertDescription>
    </Alert>
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
