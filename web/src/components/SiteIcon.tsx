import { useState } from 'react'

import { cn } from '@/lib/utils'

const avatarTones = [
  'bg-chart-1/15 text-chart-1',
  'bg-chart-2/15 text-chart-2',
  'bg-chart-3/15 text-chart-3',
  'bg-chart-4/15 text-chart-4',
  'bg-chart-5/15 text-chart-5',
] as const

function toneFor(name: string): string {
  let hash = 0
  for (const character of name) hash = (hash * 31 + character.charCodeAt(0)) | 0
  return avatarTones[Math.abs(hash) % avatarTones.length]
}

export function SiteIcon({
  iconUrl,
  name,
  size,
  className,
}: {
  iconUrl?: string
  name: string
  size: number
  className?: string
}) {
  const [failedUrl, setFailedUrl] = useState<string>()
  const style = { width: size, height: size }

  if (iconUrl && failedUrl !== iconUrl) {
    return (
      <img
        alt=""
        className={cn('shrink-0 rounded-md object-cover', className)}
        decoding="async"
        loading="lazy"
        referrerPolicy="no-referrer"
        src={iconUrl}
        style={style}
        onError={() => setFailedUrl(iconUrl)}
      />
    )
  }

  return (
    <span
      aria-hidden="true"
      className={cn(
        'flex shrink-0 items-center justify-center rounded-md font-semibold',
        size >= 32 ? 'text-sm' : 'text-xs',
        toneFor(name),
        className,
      )}
      style={style}
    >
      {name.trim().charAt(0).toLocaleUpperCase() || '?'}
    </span>
  )
}
