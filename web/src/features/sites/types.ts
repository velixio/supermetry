export type TrackingPolicy = {
  instance_mode: 'anonymous' | 'identified'
  configured_profiles_enabled: boolean
  effective_mode: 'anonymous' | 'identified'
  restrictions: string[]
}

export type RetentionDays = 0 | 7 | 30 | 90 | 365

export type SiteInput = {
  name: string
  hosts: string[]
  timezone: string
  retention_days: RetentionDays
}

export type TrackerSettings = {
  outbound: boolean
  downloads: boolean
  forms: boolean
  vitals: boolean
  not_found: boolean
  hash: boolean
  clicks: boolean
}

export type SiteUpdate = {
  tracker?: Partial<TrackerSettings>
  exclude_ips?: string[]
  exclude_paths?: string[]
  exclude_countries?: string[]
  heatmaps_enabled?: boolean
}

export type ManagedSite = {
  tracking_policy?: TrackingPolicy
  id: number
  org_id?: number
  public_id: string
  name: string
  timezone: string
  retention_days?: number
  allowed_hosts?: string[]
  last_event_received_at?: string
  icon_url?: string
  tracker?: TrackerSettings
  tracker_url?: string
  exclude_ips?: string[]
  exclude_paths?: string[]
  exclude_countries?: string[]
  heatmaps_enabled?: boolean
}
