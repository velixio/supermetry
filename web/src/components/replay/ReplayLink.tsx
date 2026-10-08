import { Link } from '@tanstack/react-router'
import { PlayIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'

export function ReplayLink({
  hasReplay,
  org,
  site,
  sessionId,
}: {
  hasReplay?: boolean
  org: string
  site: string
  sessionId?: string
}) {
  if (!hasReplay) return null
  const search = { period: '30d' as const, compare: 'off' as const, f: [], metric: 'visitors' as const, include_imported: true }
  return (
    <Button asChild aria-label="Play replay" size="icon-sm" variant="ghost">
      {sessionId ? (
        <Link to="/$org/$site/replays/$id" params={{ org, site, id: sessionId }} search={search}>
          <PlayIcon />
        </Link>
      ) : (
        <Link to="/$org/$site/replays" params={{ org, site }} search={search}>
          <PlayIcon />
        </Link>
      )}
    </Button>
  )
}
