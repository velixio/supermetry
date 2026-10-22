import { createFileRoute } from '@tanstack/react-router'

import { AdminGitHubStarsPage } from '@/features/admin/AdminGitHubStarsPage'

export const Route = createFileRoute('/app/admin/github-stars')({ component: AdminGitHubStarsPage })
