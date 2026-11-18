import { createFileRoute, redirect } from '@tanstack/react-router'

// The bare settings URL has no content of its own: open the first tab instead of an empty page.
export const Route = createFileRoute('/app/$org/$site/settings/')({
  beforeLoad: ({ params }) => {
    throw redirect({ to: '/app/$org/$site/settings/general', params: { org: params.org, site: params.site }, replace: true })
  },
})
