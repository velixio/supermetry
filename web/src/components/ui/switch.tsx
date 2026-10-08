import * as React from 'react'

import { cn } from '@/lib/utils'

type SwitchProps = Omit<React.ComponentProps<'input'>, 'onChange' | 'type'> & {
  checked?: boolean
  onCheckedChange?: (checked: boolean) => void
}

function Switch({ className, checked = false, onCheckedChange, ...props }: SwitchProps) {
  return (
    <label className="relative inline-flex h-5 w-9 shrink-0">
      <input
        type="checkbox"
        role="switch"
        checked={checked}
        data-slot="switch"
        data-state={checked ? 'checked' : 'unchecked'}
        className={cn(
          'peer absolute inset-0 appearance-none rounded-full border border-transparent shadow-xs transition-colors outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 data-[state=checked]:bg-primary data-[state=unchecked]:bg-input',
          className,
        )}
        {...props}
        onChange={(event) => onCheckedChange?.(event.currentTarget.checked)}
      />
      <span
        data-slot="switch-thumb"
        data-state={checked ? 'checked' : 'unchecked'}
        className="pointer-events-none relative m-0.5 block size-4 rounded-full bg-background shadow-sm transition-transform peer-checked:translate-x-4"
      />
    </label>
  )
}

export { Switch }
