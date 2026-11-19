import { createFileRoute, redirect } from '@tanstack/react-router'

/** The organization root has no page of its own: send people to its projects overview. */
export const Route = createFileRoute('/app/$org/')({
  beforeLoad: ({ params }) => {
    throw redirect({ to: '/app/$org/sites', params: { org: params.org }, replace: true })
  },
})
