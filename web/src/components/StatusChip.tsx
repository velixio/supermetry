import type { ComponentProps, ReactNode } from 'react'

import { cn } from '@/lib/utils'

export type StatusChipVariant = 'success' | 'warning' | 'warning-outline' | 'neutral' | 'danger'

// Text colours come from tokens that are contrast-tested in lib/contrast.test.ts.
const variants: Record<StatusChipVariant, string> = {
  success: 'border-success/40 bg-success/10 text-success',
  warning: 'border-warning/40 bg-warning/10 text-warning',
  'warning-outline': 'border-warning bg-transparent text-warning',
  neutral: 'border-border bg-muted text-muted-foreground',
  danger: 'border-destructive/40 bg-destructive/10 text-destructive',
}

type StatusChipProps = Omit<ComponentProps<'span'>, 'children'> & {
  variant?: StatusChipVariant
  /** Optional leading icon (decorative; the label carries the meaning). */
  icon?: ReactNode
  /** Always required: status is never conveyed by colour alone. */
  children: ReactNode
}

export function StatusChip({ variant = 'neutral', icon, className, children, ...props }: StatusChipProps) {
  return (
    <span
      data-slot="status-chip"
      data-variant={variant}
      className={cn(
        'inline-flex h-6 w-fit items-center gap-1 rounded-full border px-2 text-xs font-medium whitespace-nowrap [&_svg]:size-3 [&_svg]:shrink-0',
        variants[variant],
        className,
      )}
      {...props}
    >
      {icon ? <span aria-hidden="true" className="inline-flex">{icon}</span> : null}
      {children}
    </span>
  )
}
