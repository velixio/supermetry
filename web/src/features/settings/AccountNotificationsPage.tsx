import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { uiAlerts as t } from '@/messages/uiAlerts'

import { privateRequest } from './privateApi'

type Prefs = { assigned: boolean; resolved: boolean; regressed: boolean; weekly_digest: boolean }
type SitePrefs = Prefs & { site_id: number; site_name: string; site_slug: string; org_slug: string; org_name: string }

const key = ['app-notification-prefs']

const switches = (): { field: keyof Prefs; label: string; note: string }[] => [
  { field: 'assigned', label: t.accountNotifications.assigned, note: t.accountNotifications.assignedNote },
  { field: 'regressed', label: t.accountNotifications.regressed, note: t.accountNotifications.regressedNote },
  { field: 'resolved', label: t.accountNotifications.resolved, note: t.accountNotifications.resolvedNote },
  { field: 'weekly_digest', label: t.accountNotifications.weeklyDigest, note: t.accountNotifications.weeklyDigestNote },
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
    <PageContainer width="narrow" className="max-w-2xl">
      <PageHeader title={t.accountNotifications.title} description={t.accountNotifications.description} />
      {prefs.isPending && <p role="status">{t.accountNotifications.loading}</p>}
      {prefs.isError && (
        <div role="alert" className="flex flex-wrap items-center gap-3">
          <p>{t.accountNotifications.loadFailed}</p>
          <Button variant="outline" size="sm" onClick={() => void prefs.refetch()}>{t.accountNotifications.retry}</Button>
        </div>
      )}
      {save.isError && <p role="alert" className="mb-4 text-sm text-destructive">{t.accountNotifications.saveFailed}</p>}
      {prefs.isSuccess && sites.length === 0 && <p className="text-sm text-muted-foreground">{t.accountNotifications.noProjects}</p>}
      <div className="flex flex-col gap-3">
        {sites.map((site) => (
          <section key={site.site_id} aria-label={`${site.org_name}, ${site.site_name}`} className="min-w-0 rounded-lg bg-card p-4 text-card-foreground">
            <h2 className="mb-1 break-words text-sm font-medium">{site.site_name}</h2>
            <p className="mb-3 break-words text-xs text-muted-foreground">{site.org_name}</p>
            <ul className="flex flex-col gap-3">
              {switches().map(({ field, label, note }) => {
                const id = `notify-${site.site_id}-${field}`
                return (
                  <li key={field} className="flex items-center justify-between gap-4 max-md:min-h-11">
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
                        next: { assigned: site.assigned, resolved: site.resolved, regressed: site.regressed, weekly_digest: site.weekly_digest, [field]: on },
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
