import { useQuery } from '@tanstack/react-query'

import type { InstallStatus } from '@/features/sites/InstallStatus'
import { privateRequest } from '@/features/settings/privateApi'
import { useSite } from '@/features/settings/routeContext'

/**
 * Whether the site in the URL has received data (install status "receiving" or "quiet").
 * Shares its cache key with the dashboard's no-data banner, so it costs no extra request there.
 */
export function useSiteInstalled(orgSlug?: string, siteSlug?: string): boolean {
  const { membership, site } = useSite(orgSlug ?? '', siteSlug ?? '')
  const status = useQuery({
    queryKey: ['tracking-status', site?.id],
    queryFn: () => privateRequest<{ install_status?: InstallStatus }>('GET', `/api/private/sites/${site!.id}`, { orgId: membership?.org.id }),
    enabled: site !== undefined && membership !== undefined && !site.is_demo,
    staleTime: 60_000,
  })
  const value = status.data?.install_status
  return value === 'receiving' || value === 'quiet'
}
