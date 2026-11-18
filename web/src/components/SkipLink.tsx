import type { ComponentProps } from 'react'

import { cn } from '@/lib/utils'
import { uiFoundation } from '@/messages/uiFoundation'

/** Id of the element the skip link jumps to; the shell puts it on its <main>. */
export const MAIN_CONTENT_ID = 'main-content'

/** Hidden until it receives keyboard focus. Mount it as the first focusable element of the page. */
export function SkipLink({ className, children, ...props }: Omit<ComponentProps<'a'>, 'href'>) {
  return (
    <a
      href={`#${MAIN_CONTENT_ID}`}
      data-slot="skip-link"
      className={cn(
        'sr-only focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-[100] focus:rounded-lg focus:bg-primary focus:px-3 focus:py-2 focus:text-sm focus:font-medium focus:text-primary-foreground focus:ring-2 focus:ring-ring focus:ring-offset-2 focus:ring-offset-background focus:outline-none',
        className,
      )}
      {...props}
    >
      {children ?? uiFoundation.skipToContent}
    </a>
  )
}
