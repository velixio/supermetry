import type { CSSProperties, KeyboardEvent } from 'react'
import { ArrowDownIcon, ArrowUpIcon, MinusIcon, PlusIcon, SlidersHorizontalIcon, Trash2Icon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { dashEdit } from '@/messages/dashEdit'

import type { DashboardWidget } from '../api'
import { WidgetBody, WidgetBoundary } from '../widgets'
import type { WidgetContext } from '../widgets'
import { widgetLabel } from './text'

const t = dashEdit

export type SlotAction = 'up' | 'down' | 'narrower' | 'wider' | 'options' | 'remove'

type WidgetSlotProps = {
  widget: DashboardWidget
  index: number
  total: number
  preview: Omit<WidgetContext, 'widget' | 'query'>
  query: WidgetContext['query']
  onAction: (id: string, action: SlotAction) => void
}

const button = 'min-h-11 min-w-11 sm:min-h-11 sm:min-w-11'

/** One widget in the editor: a toolbar plus the live preview rendered by the viewer's widget body. */
export function WidgetSlot({ widget, index, total, preview, query, onAction }: WidgetSlotProps) {
  const name = widgetLabel(widget)
  const style = { '--span': Math.min(12, Math.max(1, widget.w)), '--wide': widget.w >= 7 ? 2 : 1 } as CSSProperties
  const act = (action: SlotAction) => onAction(widget.id, action)

  // Alt with the arrow keys moves and resizes the widget from anywhere inside its slot.
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!event.altKey || event.ctrlKey || event.metaKey) return
    const map: Record<string, SlotAction> = { ArrowUp: 'up', ArrowDown: 'down', ArrowLeft: 'narrower', ArrowRight: 'wider' }
    const action = map[event.key]
    if (!action) return
    event.preventDefault()
    act(action)
  }

  const tool = (action: SlotAction, label: string, icon: React.ReactNode, disabled = false, variant: 'outline' | 'destructive' = 'outline') => (
    <Button
      aria-disabled={disabled || undefined}
      aria-label={`${label}: ${name}`}
      className={button}
      data-action={action}
      size="icon"
      title={label}
      type="button"
      variant={variant}
      onClick={() => act(action)}
    >
      {icon}
    </Button>
  )

  return (
    <li
      className="col-span-1 min-w-0 md:col-[span_var(--wide)] lg:col-[span_var(--span)]"
      data-widget-id={widget.id}
      data-widget-kind={widget.kind}
      style={style}
    >
      <div className="dash-edit-slot flex flex-col gap-2 p-2" onKeyDown={onKeyDown}>
        <div aria-label={t.toolbar(name)} className="flex flex-wrap items-center gap-1" role="group">
          <div className="flex min-w-0 flex-1 flex-col px-1">
            <span className="truncate text-sm font-medium">{name}</span>
            <span className="text-xs text-muted-foreground">{t.position(index + 1, total)} · {t.width(widget.w)}</span>
          </div>
          {tool('up', t.moveUp, <ArrowUpIcon />, index === 0)}
          {tool('down', t.moveDown, <ArrowDownIcon />, index === total - 1)}
          {tool('narrower', t.narrower, <MinusIcon />)}
          {tool('wider', t.wider, <PlusIcon />, widget.w >= 12)}
          {tool('options', t.options, <SlidersHorizontalIcon />)}
          {tool('remove', t.remove, <Trash2Icon />, false, 'destructive')}
        </div>
        <div className="pointer-events-none min-w-0 select-none" inert>
          <WidgetBoundary title={name}>
            <WidgetBody {...preview} query={query} widget={widget} />
          </WidgetBoundary>
        </div>
      </div>
    </li>
  )
}
