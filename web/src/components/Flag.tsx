import { useState } from 'react'
import { GlobeIcon } from 'lucide-react'

import { countryFlagSrc, normalizeCountryCode } from '@/lib/country'
import { cn } from '@/lib/utils'

type FlagProps = {
  code: string
  className?: string
}

export function Flag({ code, className }: FlagProps) {
  const normalized = normalizeCountryCode(code)
  const [failedCode, setFailedCode] = useState<string>()

  if (!normalized || failedCode === normalized) {
    return (
      <GlobeIcon
        aria-hidden="true"
        className={cn('h-[15px] w-5 shrink-0 text-muted-foreground', className)}
        data-testid="flag-fallback"
      />
    )
  }

  return (
    <img
      alt=""
      aria-hidden="true"
      className={cn('h-[15px] w-5 shrink-0 rounded-[2px] object-cover ring-1 ring-border/60', className)}
      decoding="async"
      height={15}
      loading="lazy"
      src={countryFlagSrc(normalized)}
      width={20}
      onError={() => setFailedCode(normalized)}
    />
  )
}
