import { Link } from '@tanstack/react-router'
import { PlaySquareIcon } from 'lucide-react'

export function ReplayNavLink({ enabled, org, site }: { enabled: boolean; org: string; site: string }) {
  if (!enabled) return null
  return (
    <Link aria-label="Replays" to="/$org/$site/replays" params={{ org, site }} search={{ period: '30d', compare: 'off', f: [], metric: 'visitors', include_imported: true }}>
      <PlaySquareIcon />
      <span>Replays</span>
    </Link>
  )
}
