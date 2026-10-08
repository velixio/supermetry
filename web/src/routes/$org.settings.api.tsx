import { createFileRoute } from "@tanstack/react-router";

import { ApiDocsPage } from "@/features/api-docs/ApiDocsPage";
import { useOrganization } from "@/features/settings/routeContext";

export const Route = createFileRoute("/$org/settings/api")({
  component: ApiReferenceRoute,
});

function ApiReferenceRoute() {
  const { org } = Route.useParams();
  const membership = useOrganization(org);
  return membership ? (
    <ApiDocsPage orgId={membership.org.id} sites={membership.sites} />
  ) : null;
}
