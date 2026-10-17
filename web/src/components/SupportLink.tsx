import { useEffect, useState } from 'react'
import { LifeBuoyIcon } from 'lucide-react'

import { supportHref } from '@/lib/instanceConfig'
import { loadPublicConfig } from '@/lib/publicConfig'
import { cn } from '@/lib/utils'
import { uiFlows } from '@/messages/uiFlows'

/** The instance's help link (SUPERMETRY_SUPPORT_URL), in the same place on every auth and error page. Hidden when unset. */
export function SupportLink({ className, url }: { className?: string; url?: string }) {
  const [loaded, setLoaded] = useState<string | undefined>(undefined)
  useEffect(() => {
    if (url !== undefined) return
    let active = true
    void loadPublicConfig().then((config) => { if (active) setLoaded(config?.support_url) })
    return () => { active = false }
  }, [url])
  const href = supportHref(url ?? loaded)
  if (!href) return null
  const external = !href.startsWith('mailto:')
  return (
    <p className={cn('flex items-center gap-1.5 text-sm text-muted-foreground', className)}>
      <LifeBuoyIcon aria-hidden="true" className="size-4" />
      <span>{uiFlows.common.needHelp}</span>
      <a
        className="font-medium text-foreground underline underline-offset-4"
        href={href}
        {...(external ? { target: '_blank', rel: 'noreferrer' } : {})}
      >
        {uiFlows.common.contactSupport}
      </a>
    </p>
  )
}
