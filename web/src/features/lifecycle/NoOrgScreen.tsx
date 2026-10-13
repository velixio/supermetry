import { Link } from '@tanstack/react-router'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/button'
import { DeletedOrgsList } from './DeletedOrgsList'

export function NoOrgScreen({ navigate }: { navigate: (path: string) => void }) {
  return (
    <PageContainer width="narrow">
      <PageHeader
        title="You don't belong to an organization"
        description="Create one to start collecting analytics, or restore one below."
        actions={<Button asChild><Link to="/app/account/organizations/new">Create organization</Link></Button>}
      />
      <DeletedOrgsList navigate={navigate} />
    </PageContainer>
  )
}
