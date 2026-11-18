import type { ReactNode } from 'react'

import { InfoTip } from '@/components/InfoTip'
import { FieldLabel } from '@/components/ui/field'

/**
 * A field label with its secondary explanation in an InfoTip beside it.
 * The tip sits outside the label so it never becomes part of the field's accessible name.
 */
export function LabelTip({ htmlFor, children, tip, tipLabel }: { htmlFor?: string; children: ReactNode; tip: ReactNode; tipLabel?: string }) {
  return (
    <div className="flex items-center gap-1.5">
      <FieldLabel htmlFor={htmlFor}>{children}</FieldLabel>
      <InfoTip label={tipLabel ?? (typeof children === 'string' ? `About ${children}` : undefined)}>{tip}</InfoTip>
    </div>
  )
}
