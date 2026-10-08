import * as React from 'react'

import { cn } from '@/lib/utils'

function Empty({ className, ...props }: React.ComponentProps<'div'>) {
  return <div className={cn('flex min-h-52 flex-col items-center justify-center gap-4 rounded-xl border border-dashed p-6 text-center', className)} {...props} />
}

function EmptyHeader({ className, ...props }: React.ComponentProps<'div'>) {
  return <div className={cn('flex max-w-md flex-col items-center gap-2', className)} {...props} />
}

function EmptyTitle({ className, ...props }: React.ComponentProps<'h2'>) {
  return <h2 className={cn('font-medium', className)} {...props} />
}

function EmptyDescription({ className, ...props }: React.ComponentProps<'p'>) {
  return <p className={cn('text-sm text-muted-foreground', className)} {...props} />
}

export { Empty, EmptyDescription, EmptyHeader, EmptyTitle }
