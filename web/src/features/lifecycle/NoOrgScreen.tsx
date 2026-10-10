import { Link } from '@tanstack/react-router'

import { Button } from '@/components/ui/button'
import { DeletedOrgsList } from './DeletedOrgsList'

export function NoOrgScreen({ navigate }: { navigate: (path: string) => void }) {
  return (
    <section className="mx-auto flex w-full max-w-2xl flex-col gap-6 py-8">
      <header className="flex flex-col items-start gap-3 border-l-2 border-accent pl-5">
        <div className="flex flex-col gap-1">
          <h1 className="text-2xl font-semibold tracking-tight">You don't belong to an organization</h1>
          <p className="text-sm text-muted-foreground">Create one to start collecting analytics, or restore one below.</p>
        </div>
        <Button asChild><Link to="/app/account/organizations/new">Create organization</Link></Button>
      </header>
      <DeletedOrgsList navigate={navigate} />
    </section>
  )
}
