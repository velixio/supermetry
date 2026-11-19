import { useId } from 'react'

import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { fmt } from '@/lib/format'
import { importWizard } from '@/messages/importWizard'

import { dayCount, MAX_PULL_DAYS, rangeProblem, type Draft } from './draft'

const t = importWizard.range

export function RangeStep({ draft, onChange }: { draft: Draft; onChange: (patch: Partial<Draft>) => void }) {
  const id = useId()
  if (draft.mode === 'file') {
    return (
      <div className="flex flex-col gap-4">
        <h3 className="font-medium">{t.heading}</h3>
        <p className="text-sm">
          {draft.file ? t.fileSummary(draft.file.name, fmt.bytes(draft.file.size)) : t.fileNone}
        </p>
        <p className="text-sm text-muted-foreground">{t.fileRange}</p>
        <IncludedList />
      </div>
    )
  }
  const problem = rangeProblem(draft)
  const message = problem === 'invalid' ? t.invalid : problem === 'tooLong' ? t.tooLong(MAX_PULL_DAYS) : problem === 'future' ? t.future : ''
  return (
    <div className="flex flex-col gap-4">
      <h3 className="font-medium">{t.heading}</h3>
      <FieldGroup className="sm:flex-row">
        <Field>
          <FieldLabel htmlFor={`${id}-from`}>{t.from}</FieldLabel>
          <Input id={`${id}-from`} type="date" value={draft.from} aria-invalid={problem !== null}
            aria-describedby={`${id}-help`} onChange={(event) => onChange({ from: event.target.value })} />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-to`}>{t.to}</FieldLabel>
          <Input id={`${id}-to`} type="date" value={draft.to} aria-invalid={problem !== null}
            aria-describedby={`${id}-help`} onChange={(event) => onChange({ to: event.target.value })} />
        </Field>
      </FieldGroup>
      <FieldDescription id={`${id}-help`}>
        {t.rangeHelp} {problem ? null : t.days(dayCount(draft.from, draft.to))}
      </FieldDescription>
      {message ? <p role="alert" className="text-sm text-destructive">{message}</p> : null}
      <p className="text-sm text-muted-foreground">{t.sitesFixed}</p>
      <IncludedList />
    </div>
  )
}

function IncludedList() {
  return (
    <section className="flex flex-col gap-2 rounded-lg bg-muted/40 p-4 text-sm">
      <h4 className="font-medium">{t.includedHeading}</h4>
      <ul className="list-disc space-y-1 pl-5 text-muted-foreground">
        {Object.values(t.included).map((line) => <li key={line}>{line}</li>)}
      </ul>
      <p className="text-xs text-muted-foreground">{t.includedNote}</p>
    </section>
  )
}
