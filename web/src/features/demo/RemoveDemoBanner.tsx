import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { deleteSite } from '@/features/sites/api'
import type { Membership, Site } from '@/lib/me'

function storageKey(orgId: number, publicId: string): string {
  return `supermetry:demo-banner:${orgId}:${publicId}`
}

function keptDemo(orgId: number, publicId: string): boolean {
  try {
    return localStorage.getItem(storageKey(orgId, publicId)) === 'keep'
  } catch {
    return false
  }
}

export function RemoveDemoBanner({
  currentSite,
  membership,
}: {
  currentSite?: Site
  membership: Membership
}) {
  const demo = membership.sites.find((site) => site.is_demo)
  const hasRealSite = membership.sites.some((site) => !site.is_demo)
  const [hidden, setHidden] = useState(() => demo ? keptDemo(membership.org.id, demo.public_id ?? String(demo.id)) : false)
  const queryClient = useQueryClient()
  const remove = useMutation({
    mutationFn: () => deleteSite(demo!.id, membership.org.id),
    onSuccess: async () => {
      setHidden(true)
      await queryClient.invalidateQueries({ queryKey: ['me'] })
    },
  })

  if (!demo || !hasRealSite || !currentSite || currentSite.is_demo || hidden) return null

  function keep() {
    setHidden(true)
    try {
      localStorage.setItem(storageKey(membership.org.id, demo!.public_id ?? String(demo!.id)), 'keep')
    } catch {
      // Storage can be unavailable in privacy modes; the in-memory choice still applies.
    }
  }

  return (
    <Alert className="mb-4">
      <AlertTitle>Your project is ready. Remove the demo project?</AlertTitle>
      <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
        <span>Your real project is available, and the demo can be removed at any time.</span>
        <div className="flex gap-2">
          <Button type="button" size="sm" variant="destructive" disabled={remove.isPending} onClick={() => remove.mutate()}>
            {remove.isPending ? 'Removing…' : 'Remove'}
          </Button>
          <Button type="button" size="sm" variant="outline" disabled={remove.isPending} onClick={keep}>Keep</Button>
        </div>
        {remove.isError ? <span role="alert">The demo project could not be removed.</span> : null}
      </AlertDescription>
    </Alert>
  )
}
