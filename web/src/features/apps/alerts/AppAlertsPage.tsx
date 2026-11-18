import { Link } from '@tanstack/react-router'
import { useState } from 'react'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useAppAssignees } from '@/features/apps/api'
import { type AlertRule, useAlerts, useDeleteAlert, useSaveAlert } from '@/features/settings/alertsApi'
import { useChannels } from '@/features/settings/notificationApi'

import { useMonitors } from './MonitorsPage'
import { RuleBuilder } from './RuleBuilder'
import { describeRule } from './ruleTypes'

/** The rule list and builder of an app project. */
export function AppAlertsPage({ orgId, orgSlug, siteId, siteSlug }: { orgId: number; orgSlug: string; siteId: number; siteSlug: string }) {
  const alerts = useAlerts(siteId, orgId)
  const channels = useChannels(orgId)
  const assignees = useAppAssignees(siteId)
  const monitors = useMonitors(siteId)
  const save = useSaveAlert(siteId, orgId)
  const remove = useDeleteAlert(siteId, orgId)
  const [editing, setEditing] = useState<AlertRule | null | undefined>(undefined)
  const rules = (alerts.data?.data ?? []).filter((alert) => alert.kind === 'app_rule')
  const failure = alerts.error ?? channels.error ?? remove.error

  return (
    <PageContainer width="narrow">
      <PageHeader
        title="Alerts"
        description="Rules notify your team about new issues, spikes, crash rates, slow transactions and missed cron jobs."
        actions={(
          <div className="flex gap-2">
            <Button variant="outline" asChild><Link to="/app/$org/$site/settings/alert-silences" params={{ org: orgSlug, site: siteSlug }}>Silences</Link></Button>
            <Button onClick={() => setEditing(null)}>Create rule</Button>
          </div>
        )}
      />
      {failure ? <p role="alert" className="text-sm text-destructive">{failure.message}</p> : null}
      {alerts.data && rules.length === 0 ? (
        <p className="rounded-xl border bg-card p-5 text-sm text-muted-foreground">No alert rules yet. Rules notify you about new issues, spikes, crash rates, slow transactions and missed cron jobs.</p>
      ) : null}
      <div className="flex flex-col gap-3">
        {rules.map((alert) => (
          <section aria-label={`Rule ${alert.name ?? alert.id}`} className="flex flex-wrap items-center justify-between gap-3 rounded-xl border bg-card p-4" key={alert.id}>
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <h2 className="font-medium">{alert.name}</h2>
                {alert.enabled ? null : <Badge variant="secondary">Disabled</Badge>}
              </div>
              <p className="text-sm text-muted-foreground">{describeRule(alert.rule)}</p>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" asChild><Link to="/app/$org/$site/settings/notification-history" params={{ org: orgSlug, site: siteSlug }} search={{ alertId: alert.id }}>History</Link></Button>
              <Button variant="outline" onClick={() => setEditing(alert)}>Edit</Button>
              <Button variant="outline" disabled={remove.isPending} onClick={() => remove.mutate(alert.id)}>Delete</Button>
            </div>
          </section>
        ))}
      </div>
      <RuleBuilder
        alert={editing}
        channels={channels.data?.data ?? []}
        assignees={assignees.data?.members ?? []}
        monitorSlugs={(monitors.data?.monitors ?? []).map((m) => m.slug)}
        orgSlug={orgSlug}
        error={save.error}
        pending={save.isPending}
        onClose={() => { save.reset(); setEditing(undefined) }}
        onSave={async (input) => {
          await save.mutateAsync({ id: editing?.id, input })
          setEditing(undefined)
        }}
      />
    </PageContainer>
  )
}
