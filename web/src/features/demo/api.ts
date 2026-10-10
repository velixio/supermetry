import { api } from '@/lib/api'
import type { components } from '@/lib/api.gen'

export type DemoSite = components['schemas']['Site']

export class DemoApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message)
    this.name = 'DemoApiError'
  }
}

export async function createDemo(orgId: number, timezone: string): Promise<DemoSite> {
  const { data, error, response } = await api.POST('/api/private/orgs/{org}/demo', {
    params: { path: { org: orgId } },
    body: { timezone },
  })
  const demoError = error as components['schemas']['DemoError'] | undefined
  if (response.status === 409 && demoError?.site_id) {
    return { public_id: demoError.site_id } as DemoSite
  }
  if (!response.ok || !data) {
    throw new DemoApiError(
      demoError?.error === 'invalid_timezone'
        ? 'Your browser time zone is not supported.'
        : 'The demo project could not be created.',
      response.status,
    )
  }
  return data
}

export async function dismissDemoPrompt(): Promise<void> {
  const { error, response } = await api.POST('/api/private/me/demo-prompt/dismiss', {})
  if (!response.ok) {
    throw new DemoApiError(
      'The demo prompt could not be dismissed.',
      response.status,
    )
  }
  if (error) throw error
}

export async function heartbeatDemo(siteId: number): Promise<void> {
  const { error, response } = await api.POST('/api/private/sites/{site}/demo/heartbeat', {
    params: { path: { site: siteId } },
  })
  if (!response.ok) {
    throw new DemoApiError('The demo heartbeat could not be recorded.', response.status)
  }
  if (error) throw error
}
