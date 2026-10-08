import { GlobeIcon } from 'lucide-react'

import { Flag } from '@/components/Flag'
import { countryName, normalizeCountryCode } from '@/lib/country'

export function Location({ city, country }: { city?: string | null; country?: string | null }) {
  const cleanCity = city?.trim()
  const code = normalizeCountryCode(country ?? '')
  const name = code ? countryName(code) : undefined
  const label = [cleanCity, name].filter(Boolean).join(', ')

  if (!label) {
    return (
      <span className="flex items-center gap-2 whitespace-nowrap">
        <GlobeIcon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" data-testid="location-unknown-icon" />
        <span>Unknown</span>
      </span>
    )
  }

  return (
    <span className="flex items-center gap-2 whitespace-nowrap">
      {code ? <Flag code={code} className="h-3 w-4" /> : <GlobeIcon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />}
      <span>{label}</span>
    </span>
  )
}
