import { useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

import type { JourneyDirection } from './types'

type JourneyControlsProps = {
  direction: JourneyDirection
  path: string
  steps: number
  topPages: string[]
  onChange: (value: { direction: JourneyDirection; path: string; steps: number }) => void
}

const selectClass =
  'h-9 rounded-md border bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring'

export function JourneyControls({
  direction,
  path,
  steps,
  topPages,
  onChange,
}: JourneyControlsProps) {
  const [draftPath, setDraftPath] = useState(path)
  useEffect(() => setDraftPath(path), [path])
  const pathLabel = direction === 'start' ? 'Start page' : 'End page'
  const applyPath = () => onChange({ direction, path: draftPath, steps })

  return (
    <div className="grid items-end gap-4 sm:grid-cols-[1fr_2fr_1fr_auto]">
      <Field>
        <FieldLabel htmlFor="journey-direction">Journey direction</FieldLabel>
        <select
          className={selectClass}
          id="journey-direction"
          value={direction}
          onChange={(event) => onChange({ direction: event.target.value as JourneyDirection, path: draftPath, steps })}
        >
          <option value="start">Starting at</option>
          <option value="end">Ending at</option>
        </select>
      </Field>
      <Field>
        <FieldLabel htmlFor="journey-path">{pathLabel}</FieldLabel>
        <Input
          id="journey-path"
          list="journey-top-pages"
          placeholder="Any page"
          value={draftPath}
          onChange={(event) => setDraftPath(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') applyPath()
          }}
        />
        <datalist id="journey-top-pages">
          {topPages.map((page) => <option key={page} value={page}>{page}</option>)}
        </datalist>
      </Field>
      <Field>
        <FieldLabel htmlFor="journey-steps">Steps</FieldLabel>
        <select
          className={selectClass}
          id="journey-steps"
          value={steps}
          onChange={(event) => onChange({ direction, path: draftPath, steps: Number(event.target.value) })}
        >
          {[2, 3, 4, 5].map((count) => <option key={count} value={count}>{count}</option>)}
        </select>
      </Field>
      <Button type="button" onClick={applyPath}>Apply</Button>
    </div>
  )
}
