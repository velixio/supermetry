import { useQuery } from '@tanstack/react-query'

import { createAuthConfigApi, type AuthConfig } from '@/features/auth/api'

export const retentionChoices = [7, 30, 90, 365, 0] as const
export type RetentionChoice = (typeof retentionChoices)[number]

/** The public instance settings (sign-up mode, retention policy, support link). Shared and cached. */
export function useInstanceConfig() {
  return useQuery({ queryKey: ['auth-config'], queryFn: createAuthConfigApi(), staleTime: 300_000, retry: 1 })
}

export type RetentionPolicy = { defaultDays: number; forever: boolean }

/** Falls back to the long-standing defaults (30 days, forever offered) until the settings load. */
export function retentionPolicy(config?: Pick<AuthConfig, 'default_retention_days' | 'retention_forever'>): RetentionPolicy {
  const forever = config?.retention_forever !== false
  const configured = config?.default_retention_days
  const valid = typeof configured === 'number' && (retentionChoices as readonly number[]).includes(configured)
  const defaultDays = valid && (configured !== 0 || forever) ? configured : 30
  return { defaultDays, forever }
}

/**
 * The retention values a form may offer. "Keep forever" is hidden when the instance does not offer it,
 * except as the current value of a site that already keeps events forever.
 */
export function retentionOptions(policy: RetentionPolicy, current?: number): RetentionChoice[] {
  return retentionChoices.filter((days) => days !== 0 || policy.forever || current === 0)
}

/** True when moving from `from` to `to` deletes data that is currently kept. */
export function lowersRetention(from: number, to: number): boolean {
  if (from === to) return false
  if (to === 0) return false
  return from === 0 || to < from
}

/** A safe support link: https, http or mailto only. */
export function supportHref(url?: string): string | undefined {
  if (!url) return undefined
  try {
    const parsed = new URL(url)
    return ['https:', 'http:', 'mailto:'].includes(parsed.protocol) ? parsed.toString() : undefined
  } catch {
    return undefined
  }
}
