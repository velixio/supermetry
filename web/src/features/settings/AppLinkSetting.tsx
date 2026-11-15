import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { Field, FieldDescription, FieldError, FieldLabel } from '@/components/ui/field'
import { privateRequest } from '@/features/settings/privateApi'
import { nativeSelectClass } from '@/lib/nativeSelect'

type AppLink = { app_site_id: number | null }
type SiteRow = { id: number; name: string; kind?: string }

/** Lets a web site forward the diagnostic reports it receives to an app project of the same organization. */
export function AppLinkSetting({ orgId, siteId }: { orgId: number; siteId: number }) {
  const client = useQueryClient()
  const base = `/api/private/sites/${siteId}/app-link`
  const sites = useQuery({
    queryKey: ['org-sites', orgId],
    queryFn: () => privateRequest<{ data: SiteRow[] }>('GET', '/api/private/sites?limit=100', { orgId }),
  })
  const link = useQuery({ queryKey: ['app-link', siteId], queryFn: () => privateRequest<AppLink>('GET', base, { orgId }) })
  const save = useMutation({
    mutationFn: (appSiteId: number | null) => privateRequest<AppLink>('PUT', base, { orgId, body: { app_site_id: appSiteId } }),
    onSuccess: (value) => client.setQueryData(['app-link', siteId], value),
  })

  const apps = (sites.data?.data ?? []).filter((site) => site.kind === 'app')
  const current = link.data?.app_site_id ?? null
  if (link.isPending || sites.isPending || (apps.length === 0 && current === null)) return null

  return (
    <Field>
      <FieldLabel htmlFor="app-link">Send diagnostic reports to app</FieldLabel>
      <select
        id="app-link"
        className={nativeSelectClass}
        value={current ?? ''}
        disabled={save.isPending}
        onChange={(event) => save.mutate(event.target.value === '' ? null : Number(event.target.value))}
      >
        <option value="">None</option>
        {apps.map((app) => (
          <option key={app.id} value={app.id}>{app.name}</option>
        ))}
      </select>
      <FieldDescription>
        Diagnostic reports this site receives are stored in the chosen app project as crash and error reports, not as custom events.
      </FieldDescription>
      {save.error ? <FieldError>{save.error.message}</FieldError> : null}
    </Field>
  )
}
