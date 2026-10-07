import { createFileRoute } from '@tanstack/react-router'

import { en } from '@/messages/en'

export const Route = createFileRoute('/')({
  component: IndexPage,
})

function IndexPage() {
  return (
    <main className="grid min-h-dvh place-items-center px-6">
      <div className="flex max-w-sm flex-col gap-2 border-l-2 border-accent pl-5">
        <h1 className="text-2xl font-semibold tracking-tight">{en.appName}</h1>
        <p className="text-sm text-muted-foreground">{en.loadingDashboard}</p>
      </div>
    </main>
  )
}
