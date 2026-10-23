import { createFileRoute } from '@tanstack/react-router'

import { StarsRepoPage } from '@/features/github-stars/StarsRepoPage'

export const Route = createFileRoute('/app/$org/github-stars/$owner/$name')({ component: RepoRoute })

function RepoRoute() {
  const { org, owner, name } = Route.useParams()
  const navigate = Route.useNavigate()
  return <StarsRepoPage org={org} owner={owner} name={name} onRemoved={() => void navigate({ to: '/app/$org/github-stars', params: { org } })} />
}
