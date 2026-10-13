import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import type { components } from '@/lib/api.gen'
import { privateRequest } from '@/features/settings/privateApi'

type Schemas = components['schemas']
export type AdminUser = Schemas['AdminUser']
export type AdminUserPatch = Schemas['AdminUserPatch']
export type AdminOrganization = Schemas['AdminOrganization']
export type AdminOrganizationDetail = Schemas['AdminOrganizationDetail']
export type AdminSite = Schemas['AdminSite']
export type AdminSettings = Schemas['AdminSettings']
export type AdminSettingsPatch = Schemas['AdminSettingsPatch']
export type AdminTwoFactorPolicy = Schemas['AdminTwoFactorPolicy']
export type AdminTwoFactorExemption = Schemas['AdminTwoFactorExemption']
export type AdminAuditEntry = Schemas['AdminAuditEntry']
export type AdminUpdateStatus = Schemas['AdminUpdateStatus']

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
  })
}
