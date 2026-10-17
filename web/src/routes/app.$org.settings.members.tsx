import { createFileRoute } from "@tanstack/react-router";
import { MembersPage } from "@/features/settings/SettingsPages";
import { useOrganization } from "@/features/settings/routeContext";
import { useMe } from "./app.$org";

export const Route = createFileRoute("/app/$org/settings/members")({
  component: MembersRoute,
});
function MembersRoute() {
  const { org } = Route.useParams();
  const membership = useOrganization(org);
  const me = useMe();
  return membership ? (
    <MembersPage
      orgId={membership.org.id}
      orgName={membership.org.name}
      currentUserId={me.data?.user.id}
      actorRole={membership.role}
      sites={membership.sites}
    />
  ) : null;
}
