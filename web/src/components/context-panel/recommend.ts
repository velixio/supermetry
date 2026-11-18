import { pagePath } from './relatedConfig'

/** What the query cache already knows. Undefined means "not loaded", never "false". */
export type PanelFacts = {
  installStatus?: string
  goalCount?: number
  replayOff?: boolean
  rejectedEvents?: number
}

export type Recommendation = { id: 'install' | 'createGoal' | 'recordingOff' | 'rejected'; href: string }
export const MAX_RECOMMENDATIONS = 3

/** At most three actions, most urgent first, skipping the page the user is already on. */
export function recommend(facts: PanelFacts, where: { org?: string; site?: string; pathname: string }): Recommendation[] {
  const items: Recommendation[] = []
  const add = (id: Recommendation['id'], key: string) => {
    const href = pagePath(key, where.org, where.site)
    if (href && where.pathname !== href && !where.pathname.startsWith(`${href}/`)) items.push({ id, href })
  }
  if (facts.installStatus === 'waiting') add('install', 'settings/installation')
  if ((facts.rejectedEvents ?? 0) > 0) add('rejected', 'health')
  if (facts.replayOff) add('recordingOff', 'settings/replay')
  if (facts.goalCount === 0 && facts.installStatus !== undefined && facts.installStatus !== 'waiting') add('createGoal', 'goals')
  return items.slice(0, MAX_RECOMMENDATIONS)
}
