import { createApi } from '@/lib/api'

export type Problem = {
  type?: string
  title?: string
  status?: number
  detail?: string
}

export type ShareSummary = {
  totals: Record<string, number>
}

export type CreateSiteInput = {
  name: string
  hosts: string[]
  timezone: string
  retention_days: 30
}

export type CreatedSite = {
  id: number
  org_id: number
  public_id: string
  name: string
  timezone: string
  retention_days: number
  allowed_hosts: string[]
}

type ApiResult = {
  data?: unknown
  error?: unknown
  response: Response
}

type TaskApi = {
  GET: (
    path: string,
    options?: { params?: { query?: Record<string, string | number> } },
  ) => Promise<ApiResult>
  POST: (path: string, options: { body: unknown }) => Promise<ApiResult>
}

export class ShareProblem extends Error {
  readonly type: string
  readonly status?: number

  constructor(type: string, message: string, status?: number) {
    super(message)
    this.name = 'ShareProblem'
    this.type = type
    this.status = status
  }
}

function isObject(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object'
}

function problemFrom(value: unknown, response: Response): ShareProblem {
  const problem = isObject(value) ? value : {}
  const rawType = typeof problem.type === 'string' ? problem.type : ''
  const type =
    response.status === 401
      ? 'share-password-required'
      : rawType.split('/').pop() || 'share-unavailable'
  const detail =
    typeof problem.detail === 'string'
      ? problem.detail
      : 'The shared dashboard could not be opened.'
  return new ShareProblem(type, detail, response.status)
}

function sharePath(slug: string, suffix: string): string {
  return `/api/private/share/${encodeURIComponent(slug)}/${suffix}`
}

function isSummary(value: unknown): value is ShareSummary {
  if (!isObject(value) || !isObject(value.totals)) return false
  return Object.values(value.totals).every(
    (item) => typeof item === 'number' && Number.isFinite(item),
  )
}

function isCreatedSite(value: unknown): value is CreatedSite {
  if (!isObject(value)) return false
  return (
    typeof value.id === 'number' &&
    typeof value.org_id === 'number' &&
    typeof value.public_id === 'string' &&
    typeof value.name === 'string' &&
    typeof value.timezone === 'string' &&
    typeof value.retention_days === 'number' &&
    Array.isArray(value.allowed_hosts) &&
    value.allowed_hosts.every((host) => typeof host === 'string')
  )
}

export function createShareApi(fetcher?: typeof globalThis.fetch) {
  const taskApi = createApi({
    fetch: fetcher,
    onUnauthorized: () => undefined,
  }) as unknown as TaskApi

  return {
    async loadSummary(slug: string): Promise<ShareSummary> {
      const result = await taskApi.GET(sharePath(slug, 'stats/summary'), {
        params: { query: { period: '7d' } },
      })
      if (result.error || !result.response.ok) {
        throw problemFrom(result.error, result.response)
      }
      if (!isSummary(result.data)) {
        throw new ShareProblem(
          'share-invalid-response',
          'The shared dashboard returned an invalid response.',
          result.response.status,
        )
      }
      return result.data
    },

    async open(slug: string, password: string): Promise<void> {
      const result = await taskApi.POST(sharePath(slug, 'open'), {
        body: { password },
      })
      if (result.error || !result.response.ok) {
        throw problemFrom(result.error, result.response)
      }
    },

    async createSite(input: CreateSiteInput): Promise<CreatedSite> {
      const result = await taskApi.POST('/api/private/sites', { body: input })
      if (result.error || !result.response.ok) {
        const problem = problemFrom(result.error, result.response)
        throw new Error(problem.message)
      }
      if (!isCreatedSite(result.data)) {
        throw new Error('The site was created, but its response was invalid.')
      }
      return result.data
    },
  }
}

const shareApi = createShareApi()

export const loadShareSummary = shareApi.loadSummary
export const openSharedDashboard = shareApi.open
export const createFirstSite = shareApi.createSite
