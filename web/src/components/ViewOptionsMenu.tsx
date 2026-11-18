import { SlidersHorizontalIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { dashboardView } from '@/messages/dashboardView'

type ViewOptionsMenuProps = {
  includeImported: boolean
  onIncludeImportedChange: (value: boolean) => void
}

/** Rare view settings live here instead of in the filter bar; the bar shows only what is active. */
export function ViewOptionsMenu({ includeImported, onIncludeImportedChange }: ViewOptionsMenuProps) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button aria-label={dashboardView.viewOptions} className="text-muted-foreground hover:text-foreground max-sm:size-11" size="icon" title={dashboardView.viewOptions} type="button" variant="ghost">
          <SlidersHorizontalIcon aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" aria-label={dashboardView.viewOptionsMenu}>
        <DropdownMenuLabel>{dashboardView.viewOptions}</DropdownMenuLabel>
        <DropdownMenuCheckboxItem checked={includeImported} onCheckedChange={(checked) => onIncludeImportedChange(checked === true)}>
          {dashboardView.includeImported}
        </DropdownMenuCheckboxItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
