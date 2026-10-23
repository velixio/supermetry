import { createFileRoute, redirect } from '@tanstack/react-router'

// The page moved into the main app; keep old bookmarks working.
export const Route = createFileRoute('/app/admin/github-stars')({
  beforeLoad: () => { throw redirect({ to: '/app/github-stars', replace: true }) },
})
