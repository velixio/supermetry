import { createFileRoute } from '@tanstack/react-router'

import { AdminOrganizationsPage } from '@/features/admin/AdminOrganizationsPage'

export const Route = createFileRoute('/app/admin/organizations')({ component: AdminOrganizationsPage })
