import { createFileRoute } from "@tanstack/react-router";
import { ReportsPage } from "@/features/settings/SettingsPages";
import { useSite } from "@/features/settings/routeContext";

export const Route = createFileRoute("/app/$org/$site/settings/reports")({
  component: ReportsRoute,
});
function ReportsRoute() {
  const { org, site: slug } = Route.useParams();
  const { membership, site } = useSite(org, slug);
  return membership && site ? (
    <ReportsPage orgId={membership.org.id} siteId={site.id} />
  ) : null;
}
