import { createFileRoute } from "@tanstack/react-router";
import { SiteLinksPage } from "@/features/settings/SettingsPages";

export const Route = createFileRoute("/app/$org/$site/settings/funnels")({
  component: FunnelsSettingsRoute,
});
function FunnelsSettingsRoute() {
  const { org, site } = Route.useParams();
  return <SiteLinksPage org={org} site={site} kind="funnels" />;
}
