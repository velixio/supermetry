import { createFileRoute } from "@tanstack/react-router";
import { SiteGeneralPage } from "@/features/settings/SettingsPages";
import { useSite } from "@/features/settings/routeContext";

export const Route = createFileRoute("/$org/$site/settings/general")({
  component: GeneralRoute,
});
function GeneralRoute() {
  const { org, site: slug } = Route.useParams();
  const { membership, site } = useSite(org, slug);
  return membership && site ? (
    <SiteGeneralPage orgId={membership.org.id} role={membership.role} siteId={site.id} />
  ) : null;
}
