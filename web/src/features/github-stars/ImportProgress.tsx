import { fmt } from '@/lib/format'
import { en } from '@/messages/en'

/**
 * Progress of the history import of a large repository: only while it is
 * unfinished and fewer stargazers are on record than the repository has stars.
 */
export function ImportProgress({ recorded, stars, hasToken, historyComplete, className }: {
  recorded: number
  stars: number
  hasToken: boolean
  historyComplete: boolean
  className?: string
}) {
  if (historyComplete || recorded >= stars) return null
  return (
    <p role="status" className={className ?? 'text-sm text-muted-foreground'}>
      {hasToken ? en.githubStars.importProgress(fmt.full(recorded), fmt.full(stars)) : en.githubStars.importNeedsToken}
    </p>
  )
}
