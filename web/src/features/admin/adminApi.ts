import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import type { components } from '@/lib/api.gen'
import { privateRequest } from '@/features/settings/privateApi'

type Schemas = components['schemas']
export type AdminUser = Schemas['AdminUser']
export type AdminUserPatch = Schemas['AdminUserPatch']
export type AdminUserCreate = Schemas['AdminUserCreate']
export type AdminUserCreated = Schemas['AdminUserCreated']
export type AdminOrganization = Schemas['AdminOrganization']
export type AdminOrganizationDetail = Schemas['AdminOrganizationDetail']
export type AdminSite = Schemas['AdminSite']
export type AdminSettings = Schemas['AdminSettings']
export type AdminEmailSettings = Schemas['AdminEmailSettings']
export type AdminEmailSettingsInput = Schemas['AdminEmailSettingsInput']
export type AdminEmailTestResult = Schemas['AdminEmailTestResult']
export type AdminSettingsPatch = Schemas['AdminSettingsPatch']
export type AdminTwoFactorPolicy = Schemas['AdminTwoFactorPolicy']
export type AdminTwoFactorExemption = Schemas['AdminTwoFactorExemption']
export type AdminUserDetail = Schemas['AdminUserDetail']
export type AdminPasswordReset = Schemas['AdminPasswordReset']
export type AdminSystem = Schemas['AdminSystem']
export type AdminAuditEntry = Schemas['AdminAuditEntry']
export type AdminUpdateStatus = Schemas['AdminUpdateStatus']
export type AdminApplyUpdateRequest = Schemas['AdminApplyUpdateRequest']
export type AdminUpdateJob = Schemas['AdminUpdateJob']

const terminalUpdateStates = new Set<AdminUpdateJob['status']>(['complete', 'failed', 'recovery_required'])

const base = '/api/private/admin'

export const adminKeys = {
  all: ['admin'] as const,
  organization: (id: number) => ['admin', 'organization', id] as const,
  settings: ['admin', 'settings'] as const,
  email: ['admin', 'email'] as const,
  policy: ['admin', 'policy'] as const,
  audit: ['admin', 'audit'] as const,
  update: ['admin', 'update'] as const,
}

export type AdminSort = { sort?: string; order?: 'asc' | 'desc' }
type Page<T> = { data: T[]; next_cursor?: string | null }

function listPath(path: string, search: string, sort: AdminSort, cursor: string) {
  const query = new URLSearchParams({ limit: '100' })
  if (search.trim()) query.set('search', search.trim())
  if (sort.sort) query.set('sort', sort.sort)
  if (sort.order) query.set('order', sort.order)
  if (cursor) query.set('cursor', cursor)
  return `${path}?${query}`
}

/** A cursor-paged admin list: `data` is every loaded page flattened; fetchNextPage loads more. */
function useAdminList<T>(kind: string, path: string, search: string, sort: AdminSort) {
  return useInfiniteQuery({
    queryKey: ['admin', kind, search, sort.sort ?? '', sort.order ?? ''],
    initialPageParam: '',
    queryFn: ({ pageParam }) => privateRequest<Page<T>>('GET', listPath(path, search, sort, pageParam)),
    getNextPageParam: (last) => last.next_cursor || undefined,
    select: (result) => ({ data: result.pages.flatMap((page) => page.data) }),
  })
}

export function useAdminUsers(search: string, sort: AdminSort = {}) {
  return useAdminList<AdminUser>('users', `${base}/users`, search, sort)
}

export function useUpdateAdminUser() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, patch }: { id: number; patch: AdminUserPatch }) => privateRequest<AdminUser>('PATCH', `${base}/users/${id}`, { body: patch }),
    onSuccess: () => client.invalidateQueries({ queryKey: adminKeys.all }),
  })
}

export function useCreateAdminUser() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: AdminUserCreate) => privateRequest<AdminUserCreated>('POST', `${base}/users`, { body }),
    onSuccess: () => client.invalidateQueries({ queryKey: adminKeys.all }),
  })
}

/** Replaces the admin's session cookies with a one-hour session as the user. */
export function useImpersonateUser() {
  return useMutation({
    mutationFn: ({ id, password, totpCode }: { id: number; password: string; totpCode?: string }) =>
      privateRequest<void>('POST', `${base}/users/${id}/impersonate`, { body: { password, ...(totpCode ? { totp_code: totpCode } : {}) } }),
  })
}

export function useDeleteAdminOrganization() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => privateRequest<void>('DELETE', `${base}/organizations/${id}`),
    onSuccess: () => client.invalidateQueries({ queryKey: adminKeys.all }),
  })
}

export function useRestoreAdminOrganization() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => privateRequest<void>('POST', `${base}/organizations/${id}/restore`),
    onSuccess: () => client.invalidateQueries({ queryKey: adminKeys.all }),
  })
}

export function useSetAdminSiteRetention() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, retentionDays }: { id: number; retentionDays: number }) =>
      privateRequest<{ id: number; retention_days: number }>('PATCH', `${base}/sites/${id}`, { body: { retention_days: retentionDays } }),
    onSuccess: () => client.invalidateQueries({ queryKey: ['admin', 'sites'] }),
  })
}

export function useAdminOrganizations(search: string, sort: AdminSort = {}) {
  return useAdminList<AdminOrganization>('organizations', `${base}/organizations`, search, sort)
}

export function useAdminOrganization(id: number | null) {
  return useQuery({
    queryKey: adminKeys.organization(id ?? 0),
    enabled: id !== null,
    queryFn: () => privateRequest<AdminOrganizationDetail>('GET', `${base}/organizations/${id}`),
  })
}

export function useAdminSites(search: string, sort: AdminSort = {}) {
  return useAdminList<AdminSite>('sites', `${base}/sites`, search, sort)
}

export function useAdminSettings() {
  return useQuery({ queryKey: adminKeys.settings, queryFn: () => privateRequest<AdminSettings>('GET', `${base}/settings`) })
}

export function useUpdateAdminSettings() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (patch: AdminSettingsPatch) => privateRequest<AdminSettings>('PATCH', `${base}/settings`, { body: patch }),
    onSuccess: (settings) => {
      client.setQueryData(adminKeys.settings, settings)
      void client.invalidateQueries({ queryKey: adminKeys.audit })
    },
  })
}

export function useTwoFactorPolicy() {
  return useQuery({ queryKey: adminKeys.policy, queryFn: () => privateRequest<AdminTwoFactorPolicy>('GET', `${base}/security/2fa-policy`) })
}

export function useUpdateTwoFactorPolicy() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (policy: AdminTwoFactorPolicy) => privateRequest<AdminTwoFactorPolicy>('PUT', `${base}/security/2fa-policy`, { body: policy }),
    onSuccess: (policy) => {
      client.setQueryData(adminKeys.policy, policy)
      void client.invalidateQueries({ queryKey: adminKeys.audit })
    },
  })
}

export function useSetExemption() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ userId, exemption }: { userId: number; exemption: AdminTwoFactorExemption }) =>
      privateRequest<void>('PUT', `${base}/security/2fa-exemptions/${userId}`, { body: exemption }),
    onSuccess: () => client.invalidateQueries({ queryKey: adminKeys.audit }),
  })
}

export function useRemoveExemption() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (userId: number) => privateRequest<void>('DELETE', `${base}/security/2fa-exemptions/${userId}`),
    onSuccess: () => client.invalidateQueries({ queryKey: adminKeys.audit }),
  })
}

export type AuditFilters = { actor?: string; action?: string; org?: string; from?: string; to?: string }

export function useAdminAudit(filters: AuditFilters = {}) {
  return useInfiniteQuery({
    queryKey: [...adminKeys.audit, filters] as const,
    initialPageParam: '',
    queryFn: ({ pageParam }) => {
      const query = new URLSearchParams({ limit: '50' })
      for (const [key, value] of Object.entries(filters)) if (value) query.set(key, key === 'from' || key === 'to' ? new Date(value).toISOString() : value)
      if (pageParam) query.set('cursor', pageParam)
      return privateRequest<Page<AdminAuditEntry>>('GET', `${base}/audit?${query}`)
    },
    getNextPageParam: (last) => last.next_cursor || undefined,
    select: (result) => ({ data: result.pages.flatMap((page) => page.data) }),
  })
}

export function useAdminSystem() {
  return useQuery({ queryKey: ['admin', 'system'] as const, queryFn: () => privateRequest<AdminSystem>('GET', `${base}/system`), refetchInterval: 15_000 })
}

export function useAdminUser(id: number) {
  return useQuery({ queryKey: ['admin', 'user', id] as const, queryFn: () => privateRequest<AdminUserDetail>('GET', `${base}/users/${id}`) })
}

export function useRevokeUserSessions() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => privateRequest<unknown>('POST', `${base}/users/${id}/sessions/revoke`),
    onSuccess: () => client.invalidateQueries({ queryKey: ['admin', 'user'] }),
  })
}

export function useSendPasswordReset() {
  return useMutation({
    mutationFn: ({ id, password, totpCode }: { id: number; password: string; totpCode?: string }) =>
      privateRequest<AdminPasswordReset>('POST', `${base}/users/${id}/password-reset`, { body: { password, ...(totpCode ? { totp_code: totpCode } : {}) } }),
  })
}

export function useUpdateStatus(enabled: boolean) {
  return useQuery({
    queryKey: adminKeys.update,
    enabled,
    queryFn: () => privateRequest<AdminUpdateStatus>('GET', `${base}/update`),
    staleTime: 6 * 60 * 60 * 1000,
    retry: false,
    refetchOnWindowFocus: false,
    refetchInterval: (query) => {
      const job = (query.state.data as AdminUpdateStatus | undefined)?.job
      return job && !terminalUpdateStates.has(job.status) ? 2_000 : false
    },
  })
}

export function useCheckUpdate() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => privateRequest<AdminUpdateStatus>('POST', `${base}/update/check`),
    onSuccess: (status) => client.setQueryData<AdminUpdateStatus>(adminKeys.update, status),
  })
}

export function useApplyUpdate() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: AdminApplyUpdateRequest) =>
      privateRequest<AdminUpdateJob>('POST', `${base}/update/apply`, { body }),
    onSuccess: (job) => {
      client.setQueryData<AdminUpdateStatus>(adminKeys.update, (current) => current ? { ...current, job } : current)
      void client.invalidateQueries({ queryKey: adminKeys.update, exact: true })
      void client.invalidateQueries({ queryKey: adminKeys.audit })
    },
  })
}

export function useAdminEmail() {
  return useQuery({ queryKey: adminKeys.email, queryFn: () => privateRequest<AdminEmailSettings>('GET', `${base}/email`) })
}

export function useSaveAdminEmail() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: AdminEmailSettingsInput) => privateRequest<AdminEmailSettings>('PUT', `${base}/email`, { body }),
    onSuccess: (settings) => {
      client.setQueryData(adminKeys.email, settings)
      void client.invalidateQueries({ queryKey: adminKeys.audit })
    },
  })
}

export function useRemoveAdminEmail() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => privateRequest<AdminEmailSettings>('DELETE', `${base}/email`),
    onSuccess: (settings) => client.setQueryData(adminKeys.email, settings),
  })
}

export function useTestAdminEmail() {
  return useMutation({
    mutationFn: (to: string) => privateRequest<AdminEmailTestResult>('POST', `${base}/email/test`, { body: to.trim() ? { to: to.trim() } : {} }),
  })
}
