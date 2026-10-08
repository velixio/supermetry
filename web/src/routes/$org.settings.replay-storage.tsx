import { createFileRoute } from '@tanstack/react-router'

import { ReplayStoragePage } from '@/components/replay/ReplayStoragePage'

export const Route = createFileRoute('/$org/settings/replay-storage')({ component: ReplayStoragePage })
