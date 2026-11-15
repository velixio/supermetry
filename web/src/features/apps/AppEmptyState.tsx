import { Link } from '@tanstack/react-router'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'

export function AppEmptyState({ org, site }: { org: string; site: string }) {
  return (
    <PageContainer>
      <PageHeader title="No reports yet" description="Crashes, errors and compatibility reports from your app will appear here." />
      <Link className="text-sm font-medium underline" to="/app/$org/$site/settings/app" params={{ org, site }}>Set up this app</Link>
    </PageContainer>
  )
}
