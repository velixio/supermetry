import { Link } from '@tanstack/react-router'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/button'
import { en } from '@/messages/en'

/** Shown instead of the instance administration pages when an operator manages the instance. */
export function ManagedNotice() {
  return (
    <PageContainer width="narrow">
      <section aria-labelledby="managed-notice-title" className="flex flex-col items-start gap-4 py-8">
        <PageHeader titleId="managed-notice-title" title={en.admin.managedTitle} />
        <p className="text-sm text-muted-foreground">{en.admin.managedBody}</p>
        <Button asChild variant="outline" className="min-h-11"><Link to="/app">{en.admin.managedBack}</Link></Button>
      </section>
    </PageContainer>
  )
}
