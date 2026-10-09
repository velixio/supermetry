import { createFileRoute } from "@tanstack/react-router";
import { AccountSessionsPage } from "@/features/settings/SettingsPages";

export const Route = createFileRoute("/app/account/sessions")({
  component: AccountSessionsPage,
});
