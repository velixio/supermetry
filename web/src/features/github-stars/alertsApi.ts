import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import type { components } from '@/lib/api.gen'
import { privateRequest } from '@/features/settings/privateApi'
import { starsKeys } from './githubStarsApi'
import { useStarsOrg } from './starsOrg'

type Schemas = components['schemas']
export type StarsAlertSettings = Schemas['GitHubStarsAlertSettings']
export type StarsAlertInput = Schemas['GitHubStarsAlertSettingsInput']
export type StarsAlertDelivery = Schemas['GitHubStarsAlertDelivery']

const path = (orgId: number, repoId: number) => `/api/private/orgs/${orgId}/github-stars/repos/${repoId}/alerts`
const alertsKey = (orgId: number, repoId: number) => [...starsKeys.repo(orgId, repoId), 'alerts'] as const

export function useStarsAlerts(repoId: number) {
  const { orgId } = useStarsOrg()
  return useQuery({
    queryKey: alertsKey(orgId, repoId),
    queryFn: () => privateRequest<StarsAlertSettings>('GET', path(orgId, repoId), { orgId }),
  })
}

export function useSaveStarsAlerts(repoId: number) {
  const { orgId } = useStarsOrg()
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: StarsAlertInput) => privateRequest<StarsAlertSettings>('PUT', path(orgId, repoId), { orgId, body }),
    onSuccess: () => client.invalidateQueries({ queryKey: alertsKey(orgId, repoId) }),
  })
}

export function useTestStarsAlert(repoId: number) {
  const { orgId } = useStarsOrg()
  return useMutation({
    mutationFn: () => privateRequest<{ data: StarsAlertDelivery[] }>('POST', `${path(orgId, repoId)}/test`, { orgId }),
  })
}
