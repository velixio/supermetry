import { createFileRoute } from '@tanstack/react-router'

import { AdminSitesPage } from '@/features/admin/AdminSitesPage'

export const Route = createFileRoute('/app/admin/sites')({ component: AdminSitesPage })
