export type GoalInput = {
  kind: 'page' | 'event'
  match: 'exact' | 'prefix' | 'wildcard'
  value: string
  name: string
  prop_key?: string
  prop_value?: string
}

export type Goal = GoalInput & {
  id: number
  site_id: number
}
