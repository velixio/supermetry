import { useEffect, useRef, useState, type ReactNode } from 'react'

import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

export function DeferredContent({
  children,
  className,
  height,
  label,
}: {
  children: ReactNode
  className?: string
  height?: number
  label: string
}) {
  const target = useRef<HTMLElement>(null)
  const [ready, setReady] = useState(() => typeof IntersectionObserver === 'undefined')

  useEffect(() => {
    const node = target.current
    if (ready || !node || typeof IntersectionObserver === 'undefined') {
      setReady(true)
      return
    }
    const observer = new IntersectionObserver((entries) => {
      if (!entries.some((entry) => entry.isIntersecting)) return
      setReady(true)
      observer.disconnect()
    }, { rootMargin: '0px 0px -50% 0px', threshold: 0 })
    observer.observe(node)
    const idleFallback = window.setTimeout(() => setReady(true), 1_000)
    return () => {
      window.clearTimeout(idleFallback)
      observer.disconnect()
    }
  }, [ready])

  return (
    <section
      ref={target}
      aria-label={label}
      className={cn(!ready && className)}
      role="region"
      style={!ready && height ? { minHeight: height } : undefined}
    >
      {ready ? children : <Skeleton aria-hidden="true" className="size-full min-h-[inherit] rounded-xl" />}
    </section>
  )
}
