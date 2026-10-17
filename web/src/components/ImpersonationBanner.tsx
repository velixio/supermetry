import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import type { Me } from '@/lib/me'
import { requestLogout } from '@/lib/session-lifecycle'
import { ui } from '@/messages/ui'

/** Shown while an instance admin is signed in as this user; ending the session is a plain logout. */
export function ImpersonationBanner({ me }: { me: Me }) {
  if (!me.impersonated_by_email) return null
  return (
    <Alert className="mb-4 border-amber-500/50 bg-amber-50 text-amber-950 dark:bg-amber-950/40 dark:text-amber-50">
      <AlertDescription className="flex flex-wrap items-center justify-between gap-3 text-current">
        <span>{ui.impersonation.signedInAs(me.user.email)}</span>
        <Button size="sm" variant="outline" onClick={() => void requestLogout()}>
          {ui.impersonation.endSession}
        </Button>
      </AlertDescription>
    </Alert>
  )
}
