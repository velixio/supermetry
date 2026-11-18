import { useRef, type KeyboardEvent } from 'react'

import { cn } from '@/lib/utils'

export type SegmentedOption<T extends string> = { value: T; label: string; disabled?: boolean }

type SegmentedControlProps<T extends string> = {
  value: T
  onValueChange: (value: T) => void
  options: SegmentedOption<T>[]
  /** Accessible name of the group, e.g. "Chart view". Required. */
  'aria-label': string
  size?: 'sm' | 'md'
  /** Id of the panel the control switches; each option points at it via aria-controls. */
  panelId?: string
  className?: string
}

/**
 * Switches the view of one panel, so it uses tabs semantics (tablist/tab) with roving tabindex:
 * Left/Right (and Up/Down) move and select, Home/End jump. The selected segment is marked by an
 * edge at least 3:1 against the track, not by fill alone.
 */
export function SegmentedControl<T extends string>({
  value,
  onValueChange,
  options,
  size = 'md',
  panelId,
  className,
  ...rest
}: SegmentedControlProps<T>) {
  const refs = useRef<(HTMLButtonElement | null)[]>([])
  const enabled = options.map((o, i) => (o.disabled ? -1 : i)).filter((i) => i >= 0)

  function move(from: number, key: string): number | undefined {
    const pos = enabled.indexOf(from)
    if (key === 'ArrowRight' || key === 'ArrowDown') return enabled[(pos + 1) % enabled.length]
    if (key === 'ArrowLeft' || key === 'ArrowUp') return enabled[(pos - 1 + enabled.length) % enabled.length]
    if (key === 'Home') return enabled[0]
    if (key === 'End') return enabled[enabled.length - 1]
    return undefined
  }

  function onKeyDown(event: KeyboardEvent<HTMLButtonElement>, index: number) {
    const next = move(index, event.key)
    if (next === undefined) return
    event.preventDefault()
    refs.current[next]?.focus()
    onValueChange(options[next].value)
  }

  const selectedIndex = options.findIndex((o) => o.value === value)
  const tabbable = selectedIndex >= 0 && !options[selectedIndex].disabled ? selectedIndex : enabled[0]

  return (
    <div
      role="tablist"
      aria-label={rest['aria-label']}
      data-slot="segmented-control"
      data-size={size}
      className={cn(
        'inline-flex items-center rounded-lg bg-muted p-[3px] text-muted-foreground',
        size === 'sm' ? 'h-8' : 'h-9',
        className,
      )}
    >
      {options.map((option, index) => {
        const selected = option.value === value
        return (
          <button
            key={option.value}
            ref={(el) => { refs.current[index] = el }}
            type="button"
            role="tab"
            aria-selected={selected}
            aria-controls={panelId}
            tabIndex={index === tabbable ? 0 : -1}
            disabled={option.disabled}
            onClick={() => onValueChange(option.value)}
            onKeyDown={(event) => onKeyDown(event, index)}
            className={cn(
              'inline-flex min-w-6 items-center justify-center rounded-md font-medium whitespace-nowrap transition-colors outline-none border border-transparent focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-50',
              size === 'sm' ? 'h-[calc(100%-1px)] px-2 text-xs' : 'h-[calc(100%-1px)] px-2.5 text-sm',
              selected
                ? 'border-input bg-background text-foreground shadow-sm dark:bg-input/30'
                : 'text-muted-foreground hover:text-foreground',
            )}
          >
            {option.label}
          </button>
        )
      })}
    </div>
  )
}
