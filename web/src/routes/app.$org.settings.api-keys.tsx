import { createFileRoute } from "@tanstack/react-router";
import { ApiKeysPage } from "@/features/settings/keys/ApiKeysPage";
import { useOrganization } from "@/features/settings/routeContext";

export const Route = createFileRoute("/app/$org/settings/api-keys")({
  validateSearch: (search: Record<string, unknown>): { create?: 1 } =>
    search.create === 1 || search.create === "1" || search.create === true ? { create: 1 } : {},
  component: ApiKeysRoute,
});

function ApiKeysRoute() {
  const { org } = Route.useParams();
  const { create } = Route.useSearch();
  const navigate = Route.useNavigate();
  const membership = useOrganization(org);
  if (!membership) return null;
  return (
    <ApiKeysPage
      orgId={membership.org.id}
      role={membership.role}
      sites={membership.sites}
      createRequested={create === 1}
      onCreateClosed={() => void navigate({ search: {}, replace: true })}
    />
  );
}
