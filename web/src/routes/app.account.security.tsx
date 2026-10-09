import { createFileRoute } from "@tanstack/react-router";
import { AccountSecurityPage } from "@/features/settings/SettingsPages";

export const Route = createFileRoute("/app/account/security")({
  component: AccountSecurityPage,
});
