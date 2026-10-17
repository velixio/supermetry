import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AccountSecurityPage, totpSecret } from "./app.account.security";

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

  it("offers a typeable setup key and saved recovery codes before confirming", async () => {
    const request = vi.fn(async (method: string, path: string) => {
      if (method === "POST" && path === "/api/private/me/totp") return { url: "otpauth://totp/Supermetry:me?secret=JBSWY3DPEHPK3PXP&issuer=Supermetry", recovery_codes: ["aaaa-bbbb", "cccc-dddd"] };
      return undefined;
    });
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false } } })}>
        <AccountSecurityPage totpEnabled={false} request={request} />
      </QueryClientProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Set up authenticator" }));
    fireEvent.change(screen.getByLabelText("Current password"), { target: { value: "pw-123456789" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByText("JBSW Y3DP EHPK 3PXP")).toBeVisible();
    expect(screen.getByRole("button", { name: "Copy setup key" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Download codes" })).toBeVisible();
    const code = screen.getByLabelText("Code from your authenticator app");
    expect(code).toHaveAttribute("autocomplete", "one-time-code");
    const confirm = screen.getByRole("button", { name: "Confirm" });
    expect(confirm).toBeDisabled();
    fireEvent.click(screen.getByLabelText("I have saved these recovery codes"));
    expect(confirm).toBeEnabled();
    fireEvent.change(code, { target: { value: "123456" } });
    fireEvent.click(confirm);
    await waitFor(() => expect(request).toHaveBeenCalledWith("PATCH", "/api/private/me/totp", { code: "123456" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Two-factor authentication is on.");
  });

  it("reads the secret from an otpauth URI", () => {
    expect(totpSecret("otpauth://totp/x?secret=abcdefghij")).toBe("ABCD EFGH IJ");
    expect(totpSecret("not a uri")).toBe("");
  });
});
