import { createFileRoute } from "@tanstack/react-router";
import { McpPage } from "@/features/settings/keys/McpPage";
import { useOrganization } from "@/features/settings/routeContext";

export const Route = createFileRoute("/app/$org/settings/mcp")({
  component: McpRoute,
});

function McpRoute() {
  const { org } = Route.useParams();
  const membership = useOrganization(org);
  if (!membership) return null;
  return <McpPage orgId={membership.org.id} role={membership.role} sites={membership.sites} />;
}
