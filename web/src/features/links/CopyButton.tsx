import { useState } from 'react'
import { CheckIcon, CopyIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { copyText } from '@/lib/clipboard'
import { en } from '@/messages/en'

/** Copies text and announces the result to assistive technology. */
export function CopyButton({ text, label, children }: { text: string; label: string; children?: string }) {
  const [state, setState] = useState<'idle' | 'copied' | 'manual'>('idle')
  async function copy() {
    setState((await copyText(text)) ? 'copied' : 'manual')
    window.setTimeout(() => setState('idle'), 4000)
  }
  return (
    <>
      <Button aria-label={label} className="min-h-11" type="button" variant="outline" onClick={() => void copy()}>
        {state === 'copied' ? <CheckIcon data-icon="inline-start" /> : <CopyIcon data-icon="inline-start" />}
        {children ?? (state === 'copied' ? en.linksUi.copied : en.linksUi.copy)}
      </Button>
      <span className="sr-only" role="status">
        {state === 'copied' ? en.linksUi.copied : state === 'manual' ? en.linksUi.copyManual : ''}
      </span>
    </>
  )
}
