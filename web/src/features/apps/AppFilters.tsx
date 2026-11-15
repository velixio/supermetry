import { useState } from 'react'

import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import type { Period } from '@/lib/search'

import { useAppReleases } from './api'
import { APP_PERIODS, APP_PLATFORMS, type AppSearch } from './search'

const ALL = '__all__'

/** Period, release, environment and platform filters shared by every app page; state lives in the URL. */
export function AppFilters<T extends AppSearch>({ siteId, timezone, search, onSearch, hidePlatform = false }: { siteId: number; timezone: string; search: T; onSearch: (next: T) => void; hidePlatform?: boolean }) {
  // Release options come from the unfiltered releases list for the same period.
  const releases = useAppReleases(siteId, { period: search.period }, timezone)
  const [environment, setEnvironment] = useState(search.environment ?? '')
  const set = (patch: Partial<AppSearch>) => onSearch({ ...search, ...patch })
  const commitEnvironment = () => { if ((search.environment ?? '') !== environment.trim()) set({ environment: environment.trim() || undefined }) }
  const releaseOptions = releases.data?.releases.map((item) => item.release) ?? []
  if (search.release && !releaseOptions.includes(search.release)) releaseOptions.unshift(search.release)
  return (
    <div className="flex flex-wrap gap-2">
      <Select value={search.period} onValueChange={(value) => set({ period: value as Period })}>
        <SelectTrigger aria-label="Period"><SelectValue /></SelectTrigger>
        <SelectContent>{APP_PERIODS.map(([value, label]) => <SelectItem key={value} value={value}>{label}</SelectItem>)}</SelectContent>
      </Select>
      <Select value={search.release ?? ALL} onValueChange={(value) => set({ release: value === ALL ? undefined : value })}>
        <SelectTrigger aria-label="Release"><SelectValue /></SelectTrigger>
        <SelectContent><SelectItem value={ALL}>All releases</SelectItem>{releaseOptions.map((value) => <SelectItem key={value} value={value}>{value}</SelectItem>)}</SelectContent>
      </Select>
      {hidePlatform ? null : <Select value={search.platform ?? ALL} onValueChange={(value) => set({ platform: value === ALL ? undefined : value })}>
        <SelectTrigger aria-label="Platform"><SelectValue /></SelectTrigger>
        <SelectContent><SelectItem value={ALL}>All platforms</SelectItem>{APP_PLATFORMS.map((value) => <SelectItem key={value} value={value}>{value}</SelectItem>)}</SelectContent>
      </Select>}
      <Input
        aria-label="Environment" className="w-40" placeholder="Any environment" value={environment}
        onChange={(event) => setEnvironment(event.target.value)} onBlur={commitEnvironment}
        onKeyDown={(event) => { if (event.key === 'Enter') commitEnvironment() }}
      />
    </div>
  )
}
