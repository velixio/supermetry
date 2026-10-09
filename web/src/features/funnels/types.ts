export type FunnelStep = {
  type: 'page' | 'event'
  match: 'exact' | 'prefix' | 'wildcard'
  value: string
  prop_key?: string
  prop_value?: string
}

export type FunnelCountBy = 'sessions' | 'visitors'
export type FunnelWindow = 'session' | '1h' | '24h' | '7d'

export type SavedFunnel = {
  id: number
  site_id: number
  name: string
  steps: FunnelStep[]
}

export type FunnelResult = {
  count_by: FunnelCountBy
  steps: { step: number; count: number; conversion: number }[]
}
