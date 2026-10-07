import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { AccountProfilePage } from "@/features/settings/SettingsPages";
import { privateRequest } from "@/features/settings/privateApi";

export const Route = createFileRoute("/account/profile")({
  component: ProfileRoute,
});
function ProfileRoute() {
  const me = useQuery({
    queryKey: ["account"],
    queryFn: () =>
      privateRequest<{ name: string; email: string }>("GET", "/api/private/me"),
  });
  return me.data ? (
    <AccountProfilePage name={me.data.name} email={me.data.email} />
  ) : null;
}
