import { createFileRoute } from "@tanstack/react-router";
import { MembersPage } from "@/features/settings/SettingsPages";
import { useOrganization } from "@/features/settings/routeContext";

export const Route = createFileRoute("/app/$org/settings/members")({
  component: MembersRoute,
});
function MembersRoute() {
  const { org } = Route.useParams();
  const membership = useOrganization(org);
  return membership ? (
    <MembersPage
      orgId={membership.org.id}
      actorRole={membership.role}
      sites={membership.sites}
    />
  ) : null;
}
