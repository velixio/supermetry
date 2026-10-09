import { createFileRoute } from "@tanstack/react-router";
import { SiteLinksPage } from "@/features/settings/SettingsPages";

export const Route = createFileRoute("/app/$org/$site/settings/goals")({
  component: GoalsSettingsRoute,
});
function GoalsSettingsRoute() {
  const { org, site } = Route.useParams();
  return <SiteLinksPage org={org} site={site} kind="goals" />;
}
