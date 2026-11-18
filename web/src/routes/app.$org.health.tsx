import { createFileRoute } from "@tanstack/react-router";
import { HealthPage } from "@/features/health/HealthPage";
import { useOrganization } from "@/features/settings/routeContext";
import { useMe } from "./app.$org";

export const Route = createFileRoute("/app/$org/health")({ component: HealthRoute });

function HealthRoute() {
  const { org } = Route.useParams();
  const membership = useOrganization(org);
  const me = useMe();
  return membership ? <HealthPage orgId={membership.org.id} version={me.data?.version} appSites={membership.sites.filter((site) => site.kind === "app").map((site) => ({ id: site.id, name: site.name }))} /> : null;
}
