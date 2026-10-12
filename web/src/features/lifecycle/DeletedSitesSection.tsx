import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { getDeletedSites, restoreSite } from './api'
import { activeLocale } from '@/lib/i18n/intl'

function localDate(value: string): string {
  return new Intl.DateTimeFormat(activeLocale(), { dateStyle: 'medium' }).format(new Date(value))
}

function restoreError(error: unknown): string {
  const code = error instanceof Error ? error.message : ''
  if (code === 'not_restorable') return 'This can no longer be restored.'
  if (code === 'site_limit') return 'Your organization is at its site limit.'
  return 'The site could not be restored.'
}

export function DeletedSitesSection({
  orgId,
  refreshKey = '',
}: {
  orgId?: number
  refreshKey?: string
}) {
  const queryClient = useQueryClient()
  const deleted = useQuery({
    queryKey: ['lifecycle', 'deleted-sites', orgId, refreshKey],
    queryFn: () => getDeletedSites(orgId!),
    enabled: orgId !== undefined,
  })
  const restore = useMutation({
    mutationFn: (siteId: number) => restoreSite(siteId, orgId!),
    onSuccess: async () => {
      toast.success('Site restored')
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['lifecycle', 'deleted-sites', orgId] }),
        queryClient.invalidateQueries({ queryKey: ['me'] }),
      ])
    },
  })

  if (!orgId || !deleted.data?.length) return null

  return (
    <Card>
      <CardHeader>
        <CardTitle>Scheduled for deletion</CardTitle>
        <CardDescription>Restore a site before its permanent deletion date.</CardDescription>
      </CardHeader>
      <CardContent>
        {restore.error ? <p role="alert" className="mb-3 text-sm text-destructive">{restoreError(restore.error)}</p> : null}
        <ul className="flex flex-col gap-3">
          {deleted.data.map((site) => (
            <li key={site.id} className="flex flex-wrap items-center justify-between gap-3">
              <div className="min-w-0">
                <p className="truncate text-sm font-medium">{site.name}</p>
                <p className="text-sm text-muted-foreground">Deletes on {localDate(site.purge_at)}</p>
              </div>
              <Button
                size="sm"
                variant="outline"
                aria-label={`Restore ${site.name}`}
                disabled={restore.isPending}
                onClick={() => restore.mutate(site.id)}
              >
                Restore
              </Button>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}
