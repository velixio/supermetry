import { createFileRoute } from '@tanstack/react-router'

import { AdminUpdatePage } from '@/features/admin/AdminUpdatePage'

export const Route = createFileRoute('/app/admin/update')({ component: AdminUpdatePage })
