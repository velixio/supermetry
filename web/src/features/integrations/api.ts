import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import type { components } from '@/lib/api.gen'
import { privateRequest } from '@/features/settings/privateApi'

export type IntegrationEntry = components['schemas']['IntegrationCatalogEntry']
export type IntegrationConnection = components['schemas']['IntegrationConnection']
export type SearchConsoleTop = components['schemas']['SearchConsoleTop']
export type SearchConsoleRow = components['schemas']['SearchConsoleTopRow']
export type SearchConsoleDimension = SearchConsoleTop['dimension']

export const SEARCH_CONSOLE = 'search-console'

export const integrationKeys = {
  list: (siteId: number) => ['sites', siteId, 'integrations'] as const,
  top: (siteId: number, dimension: SearchConsoleDimension, from: string, to: string) =>
    ['sites', siteId, 'search-console', dimension, from, to] as const,
  report: (siteId: number) => ['sites', siteId, 'search-console'] as const,
}

export function useIntegrations(siteId: number, orgId: number) {
  return useQuery({
    queryKey: integrationKeys.list(siteId),
    queryFn: async () => {
      const result = await privateRequest<{ data: IntegrationEntry[] }>('GET', `/api/private/sites/${siteId}/integrations`, { orgId })
      return result.data
    },
  })
}

export function useConnectIntegration(siteId: number, orgId: number) {
  return useMutation({
    mutationFn: async (input: { provider: string; property: string; backfillDays: number }) => {
      const result = await privateRequest<{ authorization_url: string }>(
        'POST',
        `/api/private/sites/${siteId}/integrations/${input.provider}/connect`,
        { orgId, body: { property: input.property, backfill_days: input.backfillDays } },
      )
      return result.authorization_url
    },
  })
}

export function useDisconnectIntegration(siteId: number, orgId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (provider: string) =>
      privateRequest<void>('DELETE', `/api/private/sites/${siteId}/integrations/${provider}`, { orgId }),
    onSuccess: async () => {
      client.removeQueries({ queryKey: integrationKeys.report(siteId) })
      await client.invalidateQueries({ queryKey: integrationKeys.list(siteId) })
    },
  })
}

export function useSearchConsoleTop(
  siteId: number,
  orgId: number,
  dimension: SearchConsoleDimension,
  from: string,
  to: string,
) {
  return useQuery({
    queryKey: integrationKeys.top(siteId, dimension, from, to),
    queryFn: () =>
      privateRequest<SearchConsoleTop>(
        'GET',
        `/api/private/sites/${siteId}/search-console/top?dimension=${dimension}&from=${from}&to=${to}&limit=500`,
        { orgId },
      ),
  })
}

export type StripeIntegration = components['schemas']['StripeIntegration']
export type StripeIntegrationUpdate = components['schemas']['StripeIntegrationUpdate']

export const stripeKey = (siteId: number) => ['sites', siteId, 'stripe'] as const

function stripePath(siteId: number): string {
  return `/api/private/sites/${siteId}/integrations/stripe`
}

export function useStripe(siteId: number, orgId: number, enabled = true) {
  return useQuery({
    queryKey: stripeKey(siteId),
    queryFn: () => privateRequest<StripeIntegration>('GET', stripePath(siteId), { orgId }),
    enabled,
  })
}

function useStripeMutation<Input>(siteId: number, orgId: number, request: (input: Input) => Promise<StripeIntegration | void>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: request,
    onSuccess: async (data) => {
      if (data) client.setQueryData(stripeKey(siteId), data)
      await client.invalidateQueries({ queryKey: stripeKey(siteId) })
    },
  })
}

export function useEnableStripe(siteId: number, orgId: number) {
  return useStripeMutation<void>(siteId, orgId, () => privateRequest<StripeIntegration>('POST', stripePath(siteId), { orgId }))
}

export function useConfigureStripe(siteId: number, orgId: number) {
  return useStripeMutation<StripeIntegrationUpdate>(siteId, orgId, (body) =>
    privateRequest<StripeIntegration>('PATCH', stripePath(siteId), { orgId, body }))
}

export function useDisconnectStripe(siteId: number, orgId: number) {
  return useStripeMutation<void>(siteId, orgId, () => privateRequest<void>('DELETE', stripePath(siteId), { orgId }))
}
