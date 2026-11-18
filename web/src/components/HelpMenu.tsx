import { CircleHelpIcon, InfoIcon, KeyboardIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { ui } from '@/messages/ui'

/** The header's help button: keyboard shortcuts and About (version and credits). */
export function HelpMenu({ onOpenShortcuts, onOpenAbout }: { onOpenShortcuts: () => void; onOpenAbout: () => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button type="button" size="icon" variant="ghost" aria-label={ui.help.menu}>
          <CircleHelpIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={onOpenShortcuts}><KeyboardIcon aria-hidden="true" />{ui.help.shortcuts}</DropdownMenuItem>
        <DropdownMenuItem onSelect={onOpenAbout}><InfoIcon aria-hidden="true" />{ui.help.about}</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
