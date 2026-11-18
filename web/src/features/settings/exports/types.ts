export type ExportSchedule = {
  id: number
  site_id: number | null
  name: string
  frequency: 'daily' | 'weekly' | 'monthly'
  incremental: boolean
  enabled: boolean
  endpoint: string
  region: string
  bucket: string
  prefix: string
  access_key_id: string
  secret_set: boolean
  path_style: boolean
  next_run_at: string
  last_run_at: string | null
  last_status: 'succeeded' | 'failed' | 'skipped' | null
  last_error: string
}

export type ExportRun = {
  id: number
  started_at: string
  finished_at: string | null
  status: 'running' | 'succeeded' | 'failed' | 'skipped'
  from_day: string | null
  to_day: string | null
  bytes: number
  error: string
}

export type ScheduleDraft = {
  name: string
  site_id: number | null
  frequency: ExportSchedule['frequency']
  incremental: boolean
  enabled: boolean
  endpoint: string
  region: string
  bucket: string
  prefix: string
  access_key_id: string
  secret: string
  path_style: boolean
}

export const emptyDraft: ScheduleDraft = {
  name: '', site_id: null, frequency: 'daily', incremental: true, enabled: true, endpoint: 'https://', region: '', bucket: '', prefix: '', access_key_id: '', secret: '', path_style: false,
}

export function draftOf(schedule: ExportSchedule): ScheduleDraft {
  const { name, site_id, frequency, incremental, enabled, endpoint, region, bucket, prefix, access_key_id, path_style } = schedule
  return { name, site_id, frequency, incremental, enabled, endpoint, region, bucket, prefix, access_key_id, path_style, secret: '' }
}

/**
 * Request body for create or update. The secret is write-only: an empty secret is left out on update
 * so the stored one is kept, and the site of an existing schedule cannot change.
 */
export function scheduleBody(draft: ScheduleDraft, mode: 'create' | 'edit'): Record<string, unknown> {
  const { secret, site_id, enabled, ...rest } = draft
  if (mode === 'create') return { ...rest, ...(site_id === null ? {} : { site_id }), secret }
  return secret === '' ? { ...rest, enabled } : { ...rest, enabled, secret }
}
