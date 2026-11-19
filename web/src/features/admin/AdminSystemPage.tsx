import type { ReactNode } from 'react'

import { en } from '@/messages/en'
import { useAdminSystem } from './adminApi'
import { AdminPage, ErrorLine, formatDate } from './AdminCommon'

export function formatBytes(value: number | null | undefined) {
  if (value === null || value === undefined) return 'n/a'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let n = value
  let i = 0
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++ }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

export function formatUptime(seconds: number) {
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  return d > 0 ? `${d}d ${h}h` : h > 0 ? `${h}h ${m}m` : `${m}m`
}

function Card({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-1 rounded-lg bg-card p-4 text-sm" aria-label={title}>
      <h2 className="text-sm font-semibold">{title}</h2>
      {children}
    </section>
  )
}

export function AdminSystemPage() {
  const system = useAdminSystem()
  const s = system.data
  const pct = s ? Math.min(100, Math.round((s.database.connections / Math.max(1, s.database.max_connections)) * 100)) : 0
  return (
    <AdminPage title={en.admin.system}>
      <ErrorLine error={system.error} />
      {system.isPending ? <p role="status" className="text-sm text-muted-foreground">{en.admin.loading}</p> : null}
      {s ? (
        <div className="grid gap-3 md:grid-cols-2">
          <Card title="Version">
            <p>{s.version}</p>
            <p className="text-muted-foreground">Schema {s.schema_version} of {s.latest_schema_version}</p>
            <p className="text-muted-foreground">Uptime {formatUptime(s.uptime_seconds)}</p>
          </Card>
          <Card title="Database">
            <p>{formatBytes(s.database.size_bytes)}</p>
            <div role="progressbar" aria-label="Database connections" aria-valuemin={0} aria-valuemax={s.database.max_connections} aria-valuenow={s.database.connections} className="h-2 w-full overflow-hidden rounded bg-muted">
              <div className="h-full bg-primary" style={{ width: `${pct}%` }} />
            </div>
            <p className="text-muted-foreground">{s.database.connections} of {s.database.max_connections} connections</p>
          </Card>
          <Card title="Disk free">
            <p>Data: {formatBytes(s.data_dir?.free_bytes)} <span className="text-muted-foreground">{s.data_dir?.path}</span></p>
            <p>Replays: {formatBytes(s.replay_dir?.free_bytes)} <span className="text-muted-foreground">{s.replay_dir?.path}</span></p>
          </Card>
          <Card title="Ingest">
            <p>Queue depth {s.ingest.queue_depth.toLocaleString()}</p>
            <p>Dropped (503) {s.ingest.drops_503.toLocaleString()}</p>
            <p className="text-muted-foreground">Accepted {s.ingest.accepted_total.toLocaleString()}</p>
          </Card>
          <Card title="Jobs">
            <ul>{s.jobs.map((job) => <li key={job.name} className="flex justify-between gap-2"><span>{job.name}</span><span className="text-muted-foreground">{formatDate(job.last_run_at, 'Never run')}</span></li>)}</ul>
          </Card>
          <Card title="Email and updates">
            <p>SMTP {s.smtp_configured ? 'configured' : 'not configured'}</p>
            <p>{s.update.update_available ? `Update available: ${s.update.latest_version ?? ''}` : 'Up to date'}</p>
            {s.update.check_error ? <p className="text-destructive">{s.update.check_error}</p> : null}
            <p className="text-muted-foreground">Checked {formatDate(s.update.checked_at, 'never')}</p>
          </Card>
        </div>
      ) : null}
    </AdminPage>
  )
}
