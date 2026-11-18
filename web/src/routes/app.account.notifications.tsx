import { createFileRoute } from "@tanstack/react-router";
import { AccountNotificationsPage } from "@/features/settings/AccountNotificationsPage";

export const Route = createFileRoute("/app/account/notifications")({
  component: AccountNotificationsPage,
});
