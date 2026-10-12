import * as React from 'react'

import { cn } from '@/lib/utils'

function Card({ className, ...props }: React.ComponentProps<'section'>) {
  return <section className={cn('flex flex-col gap-4 rounded-xl border bg-card py-4 text-card-foreground', className)} {...props} />
}

function CardHeader({ className, ...props }: React.ComponentProps<'header'>) {
  return <header className={cn('grid gap-1 px-4', className)} {...props} />
}

function CardTitle({ className, ...props }: React.ComponentProps<'h2'>) {
  return <h2 className={cn('text-sm font-medium', className)} {...props} />
}

function CardDescription({ className, ...props }: React.ComponentProps<'p'>) {
  return <p className={cn('text-sm text-muted-foreground', className)} {...props} />
}

function CardContent({ className, ...props }: React.ComponentProps<'div'>) {
  return <div className={cn('px-4', className)} {...props} />
}

function CardFooter({ className, ...props }: React.ComponentProps<'footer'>) {
  return <footer className={cn('flex items-center px-4 [form>&]:mt-4', className)} {...props} />
}

export { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle }
