import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AccountSecurityPage } from "./app.account.security";

afterEach(cleanup);

type SecurityRequest = (method: "POST" | "PATCH" | "DELETE", path: string, body: Record<string, string>) => Promise<unknown>;

function renderPage(request: SecurityRequest) {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false } } })}>
      <AccountSecurityPage totpEnabled request={request} />
    </QueryClientProvider>,
  );
}

describe("account 2FA security", () => {
  it("reauthenticates before regenerating recovery codes", async () => {
    const request = vi.fn().mockResolvedValue({ recovery_codes: ["new-code"] });
    renderPage(request);

    fireEvent.click(screen.getByRole("button", { name: "Regenerate recovery codes" }));
    expect(screen.getByRole("dialog", { name: "Regenerate recovery codes" })).toBeVisible();
    fireEvent.change(screen.getByLabelText("Current password"), { target: { value: "pw-123456789" } });
    fireEvent.change(screen.getByLabelText("Authentication or recovery code"), { target: { value: "123456" } });
    fireEvent.click(screen.getByRole("button", { name: "Regenerate codes" }));

    await waitFor(() => expect(request).toHaveBeenCalledWith("POST", "/api/private/me/totp/recovery-codes", {
      current_password: "pw-123456789",
      code: "123456",
    }));
    expect(await screen.findByText("new-code")).toBeVisible();
  });

  it("reauthenticates before disabling the authenticator", async () => {
    const request = vi.fn().mockResolvedValue(undefined);
    renderPage(request);

    fireEvent.click(screen.getByRole("button", { name: "Disable authenticator" }));
    fireEvent.change(screen.getByLabelText("Current password"), { target: { value: "pw-123456789" } });
    fireEvent.change(screen.getByLabelText("Authentication or recovery code"), { target: { value: "recovery-code" } });
    fireEvent.click(screen.getByRole("button", { name: "Disable 2FA" }));

    await waitFor(() => expect(request).toHaveBeenCalledWith("DELETE", "/api/private/me/totp", {
      current_password: "pw-123456789",
      code: "recovery-code",
    }));
  });
});
