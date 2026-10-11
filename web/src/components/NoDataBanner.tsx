import { Link } from '@tanstack/react-router'

import { useStats, type StatsQuery } from '@/lib/stats'

export function NoDataBanner({ href }: { href: string }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-accent/40 bg-accent/5 px-4 py-3 text-sm" role="status">
      <span className="font-medium">No data yet</span>
      <Link className="font-medium text-accent underline-offset-4 hover:underline" to={href}>View installation steps →</Link>
    </div>
  )
}

export function DashboardNoDataBanner({ query, href }: { query: StatsQuery; href: string }) {
  const result = useStats({ ...query, metrics: ['pageviews'], dimensions: undefined })
  if (result.isLoading || result.isError || (result.data?.totals.pageviews ?? 0) > 0) return null
  return <NoDataBanner href={href} />
}
