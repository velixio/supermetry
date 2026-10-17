import { useEffect, useState } from 'react'
import { UserRoundCogIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { activeLocale } from '@/lib/i18n/intl'
import type { Me } from '@/lib/me'
import { requestLogout } from '@/lib/session-lifecycle'
import { ui } from '@/messages/ui'
import { uiFlows } from '@/messages/uiFlows'

function formatTime(value: string): string | undefined {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : date.toLocaleTimeString(activeLocale(), { hour: '2-digit', minute: '2-digit' })
}

/**
 * Stays in view at the bottom of every page while an instance admin is signed in as this user:
 * who is acting, until when, and a way out that signs out and says why on the sign-in page.
 */
export function ImpersonationBanner({ me }: { me: Me }) {
  // Interrupt once when the session starts; after that the bar is a labelled region.
  const [announced, setAnnounced] = useState(false)
  const [ending, setEnding] = useState(false)
  useEffect(() => {
    if (!me.impersonated_by_email) return
    const timer = setTimeout(() => setAnnounced(true), 1_000)
    return () => clearTimeout(timer)
  }, [me.impersonated_by_email])
  if (!me.impersonated_by_email) return null
  const endsAt = me.impersonation_expires_at ? formatTime(me.impersonation_expires_at) : undefined
  return (
    <div
      role={announced ? 'region' : 'alert'}
      aria-label={uiFlows.impersonation.region}
      data-testid="impersonation-banner"
      className="sticky bottom-0 z-40 border-t border-amber-500/60 bg-amber-50 px-4 py-2.5 text-amber-950 shadow-[0_-4px_12px_rgb(0_0_0/0.08)] dark:bg-amber-950 dark:text-amber-50"
    >
      <div className="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-3 text-sm">
        <p className="flex min-w-0 items-center gap-2">
          <UserRoundCogIcon aria-hidden="true" className="size-4 shrink-0" />
          <span>
            {uiFlows.impersonation.signedInAs(me.user.email, me.impersonated_by_email)}
            {endsAt ? ` ${uiFlows.impersonation.endsAt(endsAt)}` : ''}
          </span>
        </p>
        <Button
          size="sm"
          variant="outline"
          className="border-amber-700/40 bg-transparent text-current hover:bg-amber-100 dark:hover:bg-amber-900"
          disabled={ending}
          onClick={() => {
            setEnding(true)
            void requestLogout('/login?notice=impersonation').finally(() => setEnding(false))
          }}
        >
          {ending ? uiFlows.impersonation.ending : ui.impersonation.endSession}
        </Button>
      </div>
    </div>
  )
}
