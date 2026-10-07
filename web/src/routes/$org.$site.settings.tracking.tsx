import { createFileRoute } from "@tanstack/react-router";
import { TrackingPage } from "@/features/settings/SettingsPages";
import { useSite } from "@/features/settings/routeContext";

export const Route = createFileRoute("/$org/$site/settings/tracking")({
  component: TrackingRoute,
});
function TrackingRoute() {
  const { org, site: slug } = Route.useParams();
  const { membership, site } = useSite(org, slug);
  return membership && site ? (
    <TrackingPage
      orgId={membership.org.id}
      siteId={site.id}
      publicId={site.public_id ?? String(site.id)}
    />
  ) : null;
}
