import { createFileRoute, redirect } from '@tanstack/react-router'

export const Route = createFileRoute("/$org/$site/settings/tracking")({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: '/$org/$site/settings/installation',
      params,
      replace: true,
    })
  },
})
