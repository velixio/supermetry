import * as React from 'react'

import { cn } from '@/lib/utils'

function Table({ className, ...props }: React.ComponentProps<'table'>) {
  return <div className="relative w-full overflow-x-auto"><table className={cn('w-full text-sm', className)} {...props} /></div>
}

function TableHeader(props: React.ComponentProps<'thead'>) {
  return <thead className="[&_tr]:border-b" {...props} />
}

function TableBody(props: React.ComponentProps<'tbody'>) {
  return <tbody className="[&_tr:last-child]:border-0" {...props} />
}

function TableRow({ className, ...props }: React.ComponentProps<'tr'>) {
  return <tr className={cn('border-b transition-colors hover:bg-muted/50', className)} {...props} />
}

function TableHead({ className, ...props }: React.ComponentProps<'th'>) {
  return <th className={cn('h-10 px-3 text-left align-middle font-medium whitespace-nowrap', className)} {...props} />
}

function TableCell({ className, ...props }: React.ComponentProps<'td'>) {
  return <td className={cn('p-3 align-middle whitespace-nowrap', className)} {...props} />
}

export { Table, TableBody, TableCell, TableHead, TableHeader, TableRow }
