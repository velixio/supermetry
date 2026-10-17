import { useEffect, useRef, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'

import type { InstallStatus } from '@/features/sites/InstallStatus'
import { privateRequest } from '@/features/settings/privateApi'
import { uiFlows } from '@/messages/uiFlows'

const waitingPollMs = 10_000

export function NoDataBanner({ href }: { href: string }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-accent/40 bg-accent/5 px-4 py-3 text-sm" role="status">
      <span className="flex items-center gap-2 font-medium">
        <span aria-hidden="true" className="relative flex size-2.5">
          <span className="absolute inline-flex size-full rounded-full bg-amber-500 opacity-75 motion-safe:animate-ping" />
          <span className="relative inline-flex size-2.5 rounded-full bg-amber-500" />
        </span>
        {uiFlows.install.noDataWaiting}
      </span>
      <Link className="font-medium text-accent underline-offset-4 hover:underline" to={href}>
        {uiFlows.install.viewSteps}<span aria-hidden="true"> →</span>
      </Link>
    </div>
  )
}

/**
 * Shown while the site has never received an event (its install status, not the selected
 * period, decides). It polls, and when the first event lands it refreshes the dashboard data.
 */
export function DashboardNoDataBanner({ siteId, orgId, href, enabled = true }: { siteId: number; orgId?: number; href: string; enabled?: boolean }) {
  const queryClient = useQueryClient()
  const status = useQuery({
    queryKey: ['tracking-status', siteId],
    queryFn: () => privateRequest<{ install_status?: InstallStatus }>('GET', `/api/private/sites/${siteId}`, { orgId }),
    enabled: enabled && orgId !== undefined,
    refetchInterval: (query) => (query.state.data?.install_status === 'waiting' ? waitingPollMs : false),
  })
  const installStatus = status.data?.install_status
  const previous = useRef<InstallStatus | undefined>(undefined)
  const [arrived, setArrived] = useState(false)
  useEffect(() => {
    if (previous.current === 'waiting' && installStatus && installStatus !== 'waiting') {
      setArrived(true)
      void queryClient.invalidateQueries({ queryKey: ['stats', siteId] })
    }
    previous.current = installStatus
  }, [installStatus, queryClient, siteId])

  if (arrived) {
    return <p role="status" className="rounded-lg border border-emerald-500/40 bg-emerald-500/5 px-4 py-3 text-sm font-medium">{uiFlows.install.firstEvent}</p>
  }
  if (installStatus !== 'waiting') return null
  return <NoDataBanner href={href} />
}
