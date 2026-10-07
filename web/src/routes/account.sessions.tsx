import { createFileRoute } from "@tanstack/react-router";
import { AccountSessionsPage } from "@/features/settings/SettingsPages";

export const Route = createFileRoute("/account/sessions")({
  component: AccountSessionsPage,
});
