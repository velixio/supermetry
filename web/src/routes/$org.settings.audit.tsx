import { createFileRoute } from "@tanstack/react-router";
import { AuditPage } from "@/features/settings/SettingsPages";
import { useOrganization } from "@/features/settings/routeContext";

export const Route = createFileRoute("/$org/settings/audit")({
  component: AuditRoute,
});
function AuditRoute() {
  const { org } = Route.useParams();
  const membership = useOrganization(org);
  return membership ? <AuditPage orgId={membership.org.id} /> : null;
}
