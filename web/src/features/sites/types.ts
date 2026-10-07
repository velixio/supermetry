export type RetentionDays = 0 | 7 | 30 | 90 | 365

export type SiteInput = {
  name: string
  hosts: string[]
  timezone: string
  retention_days: RetentionDays
}

export type ManagedSite = {
  id: number
  org_id?: number
  public_id: string
  name: string
  timezone: string
  retention_days?: number
  allowed_hosts?: string[]
  last_event_received_at?: string
}
