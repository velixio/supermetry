import { createFileRoute } from "@tanstack/react-router";
import { SharesPage } from "@/features/settings/SettingsPages";
import { useSite } from "@/features/settings/routeContext";

export const Route = createFileRoute("/$org/$site/settings/shares")({
  component: SharesRoute,
});
function SharesRoute() {
  const { org, site: slug } = Route.useParams();
  const { membership, site } = useSite(org, slug);
  return membership && site ? (
    <SharesPage orgId={membership.org.id} siteId={site.id} />
  ) : null;
}
