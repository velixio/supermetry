import { useCapability } from '@/lib/authorization'
import { Link } from '@tanstack/react-router'
import { PlayIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'

export function ReplayLink({
  hasReplay,
  org,
  site,
  sessionId,
  onOpen,
}: {
  hasReplay?: boolean
  org: string
  site: string
  sessionId?: string
  onOpen?: (sessionId: string) => void
}) {
  const allowed = useCapability('replay.read')
  if (!allowed || !hasReplay) return null
  if (sessionId && onOpen) {
    return (
      <Button aria-label="Play replay" size="icon-sm" type="button" variant="ghost" onClick={() => onOpen(sessionId)}>
        <PlayIcon />
      </Button>
    )
  }
  const search = { period: '30d' as const, compare: 'off' as const, f: [], metric: 'visitors' as const, include_imported: true }
  return (
    <Button asChild aria-label="Play replay" size="icon-sm" variant="ghost">
      {sessionId ? (
        <Link to="/app/$org/$site/replays/$id" params={{ org, site, id: sessionId }} search={search}>
          <PlayIcon />
        </Link>
      ) : (
        <Link to="/app/$org/$site/replays" params={{ org, site }} search={search}>
          <PlayIcon />
        </Link>
      )}
    </Button>
  )
}
