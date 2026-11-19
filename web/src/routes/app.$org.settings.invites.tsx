import { createFileRoute } from "@tanstack/react-router";
import { InvitationsPage } from "@/features/settings/SettingsPages";
import { useOrganization } from "@/features/settings/routeContext";

export const Route = createFileRoute("/app/$org/settings/invites")({
  validateSearch: (search: Record<string, unknown>): { invite?: 1 } => (search.invite === 1 || search.invite === "1" ? { invite: 1 } : {}),
  component: InvitesRoute,
});
function InvitesRoute() {
  const { org } = Route.useParams();
  const { invite } = Route.useSearch();
  const membership = useOrganization(org);
  return membership ? <InvitationsPage orgId={membership.org.id} actorRole={membership.role} sites={membership.sites} focusInvite={invite === 1} /> : null;
}
