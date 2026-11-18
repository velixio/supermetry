import * as React from 'react'

import { cn } from '@/lib/utils'

// shadcn/ui new-york-v4 Empty classes; the title stays a heading and the description a paragraph.
function Empty({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot="empty"
      className={cn(
        'flex min-w-0 flex-1 flex-col items-center justify-center gap-6 rounded-xl border border-dashed p-6 text-center text-balance md:p-12',
        className,
      )}
      {...props}
    />
  )
}

function EmptyHeader({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot="empty-header"
      className={cn('flex max-w-sm flex-col items-center gap-2 text-center', className)}
      {...props}
    />
  )
}

function EmptyTitle({ className, ...props }: React.ComponentProps<'h2'>) {
  return <h2 data-slot="empty-title" className={cn('text-lg font-medium tracking-tight', className)} {...props} />
}

function EmptyDescription({ className, ...props }: React.ComponentProps<'p'>) {
  return (
    <p
      data-slot="empty-description"
      className={cn(
        'text-sm/relaxed text-muted-foreground [&>a]:underline [&>a]:underline-offset-4 [&>a:hover]:text-primary',
        className,
      )}
      {...props}
    />
  )
}

function EmptyContent({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot="empty-content"
      className={cn('flex w-full max-w-sm min-w-0 flex-col items-center gap-4 text-sm text-balance', className)}
      {...props}
    />
  )
}

export { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle }
