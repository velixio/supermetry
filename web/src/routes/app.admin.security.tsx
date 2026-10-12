import { createFileRoute } from '@tanstack/react-router'

import { AdminSecurityPage } from '@/features/admin/AdminSecurityPage'

export const Route = createFileRoute('/app/admin/security')({ component: AdminSecurityPage })
