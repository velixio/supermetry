import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import type { SiteKind } from '@/features/sites/types'
import { en } from '@/messages/en'

export function KindChoice({ value, onChange }: { value: SiteKind; onChange: (kind: SiteKind) => void }) {
  const options = [
    { kind: 'web' as const, title: en.pixels.website, hint: en.pixels.websiteHint },
    { kind: 'pixel' as const, title: en.pixels.anywhere, hint: en.pixels.anywhereHint },
  ]
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="sr-only">{en.pixels.measureTitle}</legend>
      <RadioGroup value={value} onValueChange={(next) => onChange(next as SiteKind)} className="grid gap-2 sm:grid-cols-2">
        {options.map((option) => (
          <Label key={option.kind} htmlFor={`kind-${option.kind}`} className="flex cursor-pointer items-start gap-3 rounded-lg border p-3 has-[[data-state=checked]]:border-primary">
            <RadioGroupItem id={`kind-${option.kind}`} value={option.kind} aria-label={`${option.title}: ${option.hint}`} />
            <span className="flex flex-col gap-0.5">
              <span className="font-medium">{option.title}</span>
              <span className="text-xs text-muted-foreground">{option.hint}</span>
            </span>
          </Label>
        ))}
      </RadioGroup>
    </fieldset>
  )
}
