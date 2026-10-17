import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

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
export type AdminSettingsPatch = Schemas['AdminSettingsPatch']
export type AdminTwoFactorPolicy = Schemas['AdminTwoFactorPolicy']
export type AdminTwoFactorExemption = Schemas['AdminTwoFactorExemption']
export type AdminAuditEntry = Schemas['AdminAuditEntry']
export type AdminUpdateStatus = Schemas['AdminUpdateStatus']
export type AdminApplyUpdateRequest = Schemas['AdminApplyUpdateRequest']
export type AdminUpdateJob = Schemas['AdminUpdateJob']

const terminalUpdateStates = new Set<AdminUpdateJob['status']>(['complete', 'failed', 'recovery_required'])

const base = '/api/private/admin'

export const adminKeys = {
  all: ['admin'] as const,
  users: (search: string) => ['admin', 'users', search] as const,
  organizations: (search: string) => ['admin', 'organizations', search] as const,
  organization: (id: number) => ['admin', 'organization', id] as const,
  sites: (search: string) => ['admin', 'sites', search] as const,
  settings: ['admin', 'settings'] as const,
  policy: ['admin', 'policy'] as const,
  audit: ['admin', 'audit'] as const,
  update: ['admin', 'update'] as const,
}

function withSearch(path: string, search: string) {
  const query = new URLSearchParams({ limit: '100' })
  if (search.trim()) query.set('search', search.trim())
  return `${path}?${query}`
}

export function useAdminUsers(search: string) {
  return useQuery({ queryKey: adminKeys.users(search), queryFn: () => privateRequest<{ data: AdminUser[] }>('GET', withSearch(`${base}/users`, search)) })
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

export function useSetAdminSiteRetention() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, retentionDays }: { id: number; retentionDays: number }) =>
      privateRequest<{ id: number; retention_days: number }>('PATCH', `${base}/sites/${id}`, { body: { retention_days: retentionDays } }),
    onSuccess: () => client.invalidateQueries({ queryKey: ['admin', 'sites'] }),
  })
}

export function useAdminOrganizations(search: string) {
  return useQuery({ queryKey: adminKeys.organizations(search), queryFn: () => privateRequest<{ data: AdminOrganization[] }>('GET', withSearch(`${base}/organizations`, search)) })
}

export function useAdminOrganization(id: number | null) {
  return useQuery({
    queryKey: adminKeys.organization(id ?? 0),
    enabled: id !== null,
    queryFn: () => privateRequest<AdminOrganizationDetail>('GET', `${base}/organizations/${id}`),
  })
}

export function useAdminSites(search: string) {
  return useQuery({ queryKey: adminKeys.sites(search), queryFn: () => privateRequest<{ data: AdminSite[] }>('GET', withSearch(`${base}/sites`, search)) })
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

export function useAdminAudit() {
  return useQuery({ queryKey: adminKeys.audit, queryFn: () => privateRequest<{ data: AdminAuditEntry[] }>('GET', `${base}/audit?limit=50`) })
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
      void client.invalidateQueries({ queryKey: adminKeys.audit, exact: true })
    },
  })
}
