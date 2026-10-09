import { createFileRoute } from "@tanstack/react-router";
import { InvitationsPage } from "@/features/settings/SettingsPages";
import { useOrganization } from "@/features/settings/routeContext";

export const Route = createFileRoute("/$org/settings/invites")({
  component: InvitesRoute,
});
function InvitesRoute() {
  const { org } = Route.useParams();
  const membership = useOrganization(org);
  return membership ? <InvitationsPage orgId={membership.org.id} actorRole={membership.role} sites={membership.sites} /> : null;
}
