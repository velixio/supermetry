export type GoalInput = {
  kind: 'page' | 'event' | 'scroll'
  match: 'exact' | 'prefix' | 'wildcard'
  value: string
  name: string
  prop_key?: string
  prop_value?: string
  currency?: string
  scroll_threshold?: number
}

export type Goal = GoalInput & {
  id: number
  site_id: number
}
