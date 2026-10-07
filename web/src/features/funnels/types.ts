export type FunnelStep = {
  type: 'page' | 'event'
  match: 'exact' | 'prefix' | 'wildcard'
  value: string
}

export type SavedFunnel = {
  id: number
  site_id: number
  name: string
  steps: FunnelStep[]
}

export type FunnelResult = {
  steps: { step: number; visitors: number; conversion: number }[]
}
