import type { ReactNode } from 'react'
import { ListFilterIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import { useIsMobile } from '@/hooks/use-mobile'
import { en } from '@/messages/en'

export function ResponsiveFilters({ children }: { children: ReactNode }) {
  const mobile = useIsMobile()
  if (!mobile) return <div className="flex flex-wrap items-center justify-end gap-2">{children}</div>

  return (
    <Sheet>
      <SheetTrigger asChild>
        <Button className="min-h-11" type="button" variant="outline"><ListFilterIcon data-icon="inline-start" />{en.polish.filters}</Button>
      </SheetTrigger>
      <SheetContent className="w-[min(23rem,100vw)] overflow-y-auto" side="right">
        <SheetHeader>
          <SheetTitle>{en.polish.filters}</SheetTitle>
          <SheetDescription>{en.polish.filtersDescription}</SheetDescription>
        </SheetHeader>
        <div className="flex flex-col items-stretch gap-4 px-4 pb-6">{children}</div>
      </SheetContent>
    </Sheet>
  )
}
