import { CheckIcon, CopyIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { useCopyStatus } from '@/features/settings/InstallRecipes'
import { uiSettings } from '@/messages/uiSettings'

/** A code block with a Copy button; the result is announced through a polite live region. */
export function CopyBlock({ value, what, className }: { value: string; what: string; className?: string }) {
  const { copied, copy } = useCopyStatus()
  const t = uiSettings.mcp
  return (
    <div className={className}>
      <div className="relative">
        <pre className="overflow-x-auto rounded-lg bg-muted p-3 pr-24 text-xs"><code>{value}</code></pre>
        <Button type="button" size="sm" variant="outline" className="absolute top-2 right-2 min-h-8" aria-label={t.blockLabel(what)} onClick={() => void copy(value)}>
          {copied === 'yes' ? <CheckIcon data-icon="inline-start" /> : <CopyIcon data-icon="inline-start" />}
          {t.copy}
        </Button>
      </div>
      <span role="status" aria-live="polite" className="sr-only">{copied === 'yes' ? t.copied : copied === 'manual' ? t.copyFailed : ''}</span>
      {copied === 'manual' ? <p className="mt-1 text-xs text-muted-foreground">{t.copyFailed}</p> : null}
    </div>
  )
}
