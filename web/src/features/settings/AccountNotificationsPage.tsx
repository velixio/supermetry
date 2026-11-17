import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'

import { privateRequest } from './privateApi'

type Prefs = { assigned: boolean; resolved: boolean; regressed: boolean }
type SitePrefs = Prefs & { site_id: number; site_name: string; site_slug: string; org_slug: string; org_name: string }

const key = ['app-notification-prefs']

const switches: { field: keyof Prefs; label: string; note: string }[] = [
  { field: 'assigned', label: 'Assigned to me', note: 'An issue is assigned to you by someone else.' },
  { field: 'regressed', label: 'Regressed', note: 'An issue assigned to you comes back after it was resolved.' },
  { field: 'resolved', label: 'Resolved', note: 'Someone else resolves an issue assigned to you.' },
]

// Per-user, per-project switches for the issue emails. They only ever change
// the signed-in user's own preferences.
export function AccountNotificationsPage() {
  const queryClient = useQueryClient()
  const prefs = useQuery({
    queryKey: key,
    queryFn: () => privateRequest<{ sites: SitePrefs[] }>('GET', '/api/private/me/app-notifications'),
  })
  const save = useMutation({
    mutationFn: ({ site, next }: { site: number; next: Prefs }) =>
      privateRequest<Prefs>('PUT', `/api/private/me/app-notifications/${site}`, { body: next }),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: key }),
  })
  const sites = prefs.data?.sites ?? []

  return (
    <PageContainer width="narrow">
      <PageHeader title="Notifications" description="Choose which app issue emails you receive. Emails only go to the person an issue is assigned to, never to the person who made the change." />
      {prefs.isPending && <p role="status">Loading notification settings…</p>}
      {prefs.isError && (
        <div role="alert" className="flex flex-wrap items-center gap-3">
          <p>Could not load your notification settings.</p>
          <Button variant="outline" size="sm" onClick={() => void prefs.refetch()}>Try again</Button>
        </div>
      )}
      {save.isError && <p role="alert" className="mb-4 text-sm text-destructive">Could not save the change. Try again.</p>}
      {prefs.isSuccess && sites.length === 0 && <p className="text-sm text-muted-foreground">You have no app projects yet.</p>}
      <div className="flex flex-col gap-4">
        {sites.map((site) => (
          <section key={site.site_id} aria-label={`${site.org_name}, ${site.site_name}`} className="min-w-0 rounded-xl border bg-card p-5 text-card-foreground">
            <h2 className="mb-1 break-words text-sm font-medium">{site.site_name}</h2>
            <p className="mb-4 break-words text-xs text-muted-foreground">{site.org_name}</p>
            <ul className="divide-y">
              {switches.map(({ field, label, note }) => {
                const id = `notify-${site.site_id}-${field}`
                return (
                  <li key={field} className="flex items-center justify-between gap-4 py-3 first:pt-0 last:pb-0">
                    <div className="min-w-0">
                      <label htmlFor={id} className="text-sm font-medium">{label}</label>
                      <p className="text-xs text-muted-foreground">{note}</p>
                    </div>
                    <Switch
                      id={id}
                      checked={site[field]}
                      disabled={save.isPending}
                      onCheckedChange={(on) => save.mutate({
                        site: site.site_id,
                        next: { assigned: site.assigned, resolved: site.resolved, regressed: site.regressed, [field]: on },
                      })}
                    />
                  </li>
                )
              })}
            </ul>
          </section>
        ))}
      </div>
    </PageContainer>
  )
}
