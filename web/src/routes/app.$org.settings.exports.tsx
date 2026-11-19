import { createFileRoute } from "@tanstack/react-router";
import { ExportsPage } from "@/features/settings/exports/ExportsPage";
import { useOrganization } from "@/features/settings/routeContext";

export const Route = createFileRoute("/app/$org/settings/exports")({
  component: ExportsRoute,
});

function ExportsRoute() {
  const { org } = Route.useParams();
  const membership = useOrganization(org);
  if (!membership) return null;
  return <ExportsPage orgId={membership.org.id} role={membership.role} sites={membership.sites} />;
}
