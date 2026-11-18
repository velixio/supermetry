import { useState, type ReactNode } from 'react'
import { InfoIcon } from 'lucide-react'

import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

/**
 * Secondary explanation kept off the page: a small info icon whose text shows on hover, keyboard focus or tap.
 * Use it for detail a first-time reader may need but a regular one does not; the essential sentence stays visible.
 * Focus opens it, and while open the tooltip is the button's accessible description.
 */
export function InfoTip({ children, label = 'More info', className }: { children: ReactNode; label?: string; className?: string }) {
  const [open, setOpen] = useState(false)
  return (
    <Tooltip open={open} onOpenChange={setOpen}>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label={label}
          className={cn('inline-flex size-4 shrink-0 items-center justify-center rounded-sm align-middle text-muted-foreground hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none', className)}
          // Touch screens have no hover: a tap toggles the tip.
          onClick={() => setOpen((value) => !value)}
          data-slot="info-tip"
        >
          <InfoIcon aria-hidden="true" className="size-3.5" />
        </button>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs text-pretty">{children}</TooltipContent>
    </Tooltip>
  )
}
