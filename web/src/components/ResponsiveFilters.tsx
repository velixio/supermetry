import type { ReactNode } from 'react'
import { ListFilterIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import { useIsMobile } from '@/hooks/use-mobile'
import { cn } from '@/lib/utils'
import { en } from '@/messages/en'

/**
 * `lead` (the date and compare controls) stays visible; on narrow screens it shares one scrollable row with the
 * Filters button, and the remaining controls move into the sheet.
 */
export function ResponsiveFilters({ children, className, lead }: { children: ReactNode; className?: string; lead?: ReactNode }) {
  const mobile = useIsMobile()
  if (!mobile) return <div className={cn('flex flex-wrap items-center justify-end gap-2', className)}>{lead}{children}</div>

  return (
    <div className="flex min-w-0 items-center gap-2 overflow-x-auto">
    <Sheet>
      <SheetTrigger asChild>
        <Button className="max-sm:min-h-11 shrink-0" type="button" variant="outline"><ListFilterIcon data-icon="inline-start" />{en.polish.filters}</Button>
      </SheetTrigger>
      <SheetContent className="w-[min(23rem,100vw)] overflow-y-auto" side="right">
        <SheetHeader>
          <SheetTitle>{en.polish.filters}</SheetTitle>
          <SheetDescription>{en.polish.filtersDescription}</SheetDescription>
        </SheetHeader>
        <div className="flex flex-col items-stretch gap-4 px-4 pb-6">{children}</div>
      </SheetContent>
    </Sheet>
    {lead}
    </div>
  )
}
