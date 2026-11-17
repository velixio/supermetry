import { createFileRoute } from '@tanstack/react-router'

import { AdminEmailPage } from '@/features/admin/AdminEmailPage'

export const Route = createFileRoute('/app/admin/email')({ component: AdminEmailPage })
