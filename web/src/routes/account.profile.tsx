import { createFileRoute } from "@tanstack/react-router";
import { AccountProfilePage } from "@/features/settings/AccountProfilePage";
import { useMe } from "./$org";

export const Route = createFileRoute("/account/profile")({
  component: ProfileRoute,
});
function ProfileRoute() {
  const me = useMe();
  return me.data ? (
    <AccountProfilePage name={me.data.user.name} email={me.data.user.email} totpEnabled={Boolean(me.data.user.totp_enabled)} onDeleted={() => window.location.assign('/login')} />
  ) : null;
}
