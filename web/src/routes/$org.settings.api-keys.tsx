import { createFileRoute } from "@tanstack/react-router";
import { ApiKeysPage } from "@/features/settings/SettingsPages";
import { useOrganization } from "@/features/settings/routeContext";

export const Route = createFileRoute("/$org/settings/api-keys")({
  component: ApiKeysRoute,
});
function ApiKeysRoute() {
  const { org } = Route.useParams();
  const membership = useOrganization(org);
  return membership ? <ApiKeysPage orgId={membership.org.id} /> : null;
}
