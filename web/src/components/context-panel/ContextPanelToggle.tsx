import { PanelRightIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { isApplePlatform } from '@/lib/shortcuts'
import { contextPanel as t } from '@/messages/contextPanel'

import { useContextPanel } from './ContextPanelProvider'

/** Ghost icon button at the end of the app header. Renders nothing without a ContextPanelProvider. */
export function ContextPanelToggle() {
  const { available, open, panelId, toggle, toggleRef } = useContextPanel()
  if (!available) return null
  const hint = isApplePlatform() ? '⌘ .' : 'Ctrl .'
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          ref={toggleRef}
          type="button"
          variant="ghost"
          size="icon-sm"
          className="text-muted-foreground max-md:size-11"
          aria-label={t.label}
          aria-pressed={open}
          aria-controls={panelId}
          aria-keyshortcuts="Control+. Meta+."
          onClick={toggle}
        >
          <PanelRightIcon aria-hidden="true" />
        </Button>
      </TooltipTrigger>
      <TooltipContent>
        {t.label}
        <kbd className="ml-2 rounded-sm bg-background/20 px-1 text-[10px] font-medium">{hint}</kbd>
      </TooltipContent>
    </Tooltip>
  )
}
