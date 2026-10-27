import { createFileRoute } from '@tanstack/react-router'

import { canManageStars } from '@/features/github-stars/starsOrg'
import { StarsRepoPage } from '@/features/github-stars/StarsRepoPage'
import { useOrganization } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/github-stars/$owner/$name')({ component: RepoRoute })

function RepoRoute() {
  const { org, owner, name } = Route.useParams()
  const navigate = Route.useNavigate()
  const membership = useOrganization(org)
  if (!membership) return null
  return (
    <StarsRepoPage
      org={org} orgId={membership.org.id} canManage={canManageStars(membership.role)} owner={owner} name={name}
      onRemoved={() => void navigate({ to: '/app/$org/github-stars', params: { org } })}
    />
  )
}
