import { useQuery } from '@tanstack/react-query'

type Source = 'dbip' | 'maxmind'

// The credit each geolocation provider's license asks for. DB-IP's database is bundled with
// Supermetry; MaxMind's GeoLite data is downloaded by the operator when they configure an
// account, and its license says to credit MaxMind wherever the data is used.
async function fetchGeoIPSource(): Promise<Source> {
  const response = await fetch('/api/private/auth/config', { credentials: 'same-origin' })
  if (!response.ok) throw new Error('config unavailable')
  const config = (await response.json()) as { geoip_source?: unknown }
  return config.geoip_source === 'maxmind' ? 'maxmind' : 'dbip'
}

export function GeolocationCredit() {
  const { data, isPending, isError } = useQuery({
    queryKey: ['auth-config', 'geoip-source'],
    queryFn: fetchGeoIPSource,
    staleTime: Infinity,
    retry: false,
  })

  // Show nothing while the source is unknown, so the wrong provider is never credited.
  if (isPending) return null
  const source: Source = isError ? 'dbip' : data

  if (source === 'maxmind') {
    return (
      <>
        This product includes GeoLite Data created by MaxMind, available from{' '}
        <a
          className="underline-offset-4 hover:text-foreground hover:underline"
          href="https://www.maxmind.com"
          rel="noreferrer"
          target="_blank"
        >
          https://www.maxmind.com
        </a>
        .
      </>
    )
  }
  return (
    <a className="underline-offset-4 hover:text-foreground hover:underline" href="https://db-ip.com">
      IP geolocation by DB-IP.com
    </a>
  )
}
