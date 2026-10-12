import { createFileRoute } from '@tanstack/react-router'

import { AdminSettingsPage } from '@/features/admin/AdminSettingsPage'

export const Route = createFileRoute('/app/admin/settings')({ component: AdminSettingsPage })
