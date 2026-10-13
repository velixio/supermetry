import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { SiteGeneralPage } from "@/features/settings/SettingsPages";
import { useSite } from "@/features/settings/routeContext";

export const Route = createFileRoute("/app/$org/$site/settings/general")({
  component: GeneralRoute,
});
function GeneralRoute() {
  const { org, site: slug } = Route.useParams();
  const { membership, site } = useSite(org, slug);
  const navigate = useNavigate();
  return membership && site ? (
    <SiteGeneralPage
      orgId={membership.org.id}
      role={membership.role}
      siteId={site.id}
      onSlugChange={(next) =>
        void navigate({ to: "/app/$org/$site/settings/general", params: { org, site: next }, replace: true })
      }
    />
  ) : null;
}
