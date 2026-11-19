import { CheckIcon } from 'lucide-react'

import { cn } from '@/lib/utils'
import { importWizard } from '@/messages/importWizard'

import { wizardSteps, type WizardStep } from './draft'

const t = importWizard

/** Progress through the wizard; the current step is announced with aria-current. */
export function WizardStepper({ current }: { current: WizardStep }) {
  const index = wizardSteps.indexOf(current)
  return (
    <nav aria-label={t.stepperLabel}>
      <ol className="flex flex-wrap gap-x-4 gap-y-2 text-sm">
        {wizardSteps.map((step, position) => {
          const done = position < index
          const active = position === index
          return (
            <li key={step} className="flex items-center gap-2" {...(active ? { 'aria-current': 'step' as const } : {})}>
              <span
                aria-hidden="true"
                className={cn(
                  'flex size-6 items-center justify-center rounded-full bg-muted text-xs font-medium',
                  active && 'bg-primary text-primary-foreground',
                  done && 'bg-success/10 text-success',
                )}
              >
                {done ? <CheckIcon className="size-3" /> : position + 1}
              </span>
              <span className={cn(active ? 'font-medium' : 'text-muted-foreground')}>
                <span className="sr-only">{t.stepOf(position + 1, wizardSteps.length, '')}</span>
                {t.steps[step]}
              </span>
            </li>
          )
        })}
      </ol>
    </nav>
  )
}
