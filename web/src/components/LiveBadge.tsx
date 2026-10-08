import { cn } from '@/lib/utils'

type LiveBadgeProps = {
  count: number
  enabled: boolean
  label: (count: number) => string
  enableLabel: string
  disableLabel: string
  muted?: boolean
  onToggle?: () => void
}

export function LiveBadge({
  count,
  enabled,
  label,
  enableLabel,
  disableLabel,
  muted = false,
  onToggle,
}: LiveBadgeProps) {
  const content = (
    <>
      <span aria-hidden="true" className="text-accent">
        ●{' '}
      </span>
      <span>{label(count)}</span>
    </>
  )
  const className = cn(
    'inline-flex h-8 items-center gap-2 rounded-md border px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring',
    muted && 'text-muted-foreground',
  )

  return onToggle ? (
    <button
      aria-label={enabled ? disableLabel : enableLabel}
      aria-pressed={enabled}
      className={className}
      type="button"
      onClick={onToggle}
    >
      {content}
    </button>
  ) : (
    <output aria-live="polite" className={className}>
      {content}
    </output>
  )
}
