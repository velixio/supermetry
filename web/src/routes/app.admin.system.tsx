import { createFileRoute } from '@tanstack/react-router'

import { AdminSystemPage } from '@/features/admin/AdminSystemPage'

export const Route = createFileRoute('/app/admin/system')({ component: AdminSystemPage })
