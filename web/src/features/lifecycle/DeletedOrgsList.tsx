import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { getDeletedOrganizations, restoreOrganization, type DeletedOrganization } from './api'

function localDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' }).format(new Date(value))
}

function restoreError(error: unknown): string {
  const code = error instanceof Error ? error.message : ''
  if (code === 'not_restorable') return 'This can no longer be restored.'
  if (code === 'site_limit') return 'Your organization is at its site limit.'
  return 'The organization could not be restored.'
}

export function DeletedOrgsList({
  navigate,
}: {
  navigate: (path: string) => void
}) {
  const queryClient = useQueryClient()
  const deleted = useQuery({
    queryKey: ['lifecycle', 'deleted-organizations'],
    queryFn: getDeletedOrganizations,
  })
  const restore = useMutation({
    mutationFn: (organization: DeletedOrganization) => restoreOrganization(organization.id).then(() => organization),
    onSuccess: async (organization) => {
      toast.success('Organization restored')
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['lifecycle', 'deleted-organizations'] }),
        queryClient.invalidateQueries({ queryKey: ['me'] }),
      ])
      navigate(`/app/${organization.slug}/settings`)
    },
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle>Organizations scheduled for deletion</CardTitle>
        <CardDescription>Restore an organization before its permanent deletion date.</CardDescription>
      </CardHeader>
      <CardContent>
        {restore.error ? <p role="alert" className="mb-3 text-sm text-destructive">{restoreError(restore.error)}</p> : null}
        {deleted.data?.length ? (
          <ul className="flex flex-col gap-4">
            {deleted.data.map((organization) => (
              <li key={organization.id} className="flex flex-wrap items-center justify-between gap-3">
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium">{organization.name}</p>
                  <p className="text-sm text-muted-foreground">Deletes on {localDate(organization.purge_at)}</p>
                  {organization.sites.length ? (
                    <p className="truncate text-xs text-muted-foreground">Projects: {organization.sites.join(', ')}</p>
                  ) : null}
                </div>
                <Button
                  size="sm"
                  variant="outline"
                  aria-label={`Restore ${organization.name}`}
                  disabled={restore.isPending}
                  onClick={() => restore.mutate(organization)}
                >
                  Restore
                </Button>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-muted-foreground">No organizations are scheduled for deletion.</p>
        )}
      </CardContent>
    </Card>
  )
}
