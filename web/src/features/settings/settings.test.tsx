import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ApiKeySecretDialog } from "./ApiKeySecretDialog";
import { MemberRoleSelect } from "./MemberRoleSelect";
import { TotpQr } from "./TotpQr";
import { TrackingSnippet } from "./TrackingSnippet";

afterEach(cleanup);

describe("settings security boundaries", () => {
  it("removes an API key secret from the DOM when the shown-once dialog closes", () => {
    render(
      <ApiKeySecretDialog
        secret="pnl_demo_only_once"
        onClose={() => undefined}
      />,
    );

    expect(screen.getByText("pnl_demo_only_once")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /close/i }));

    expect(screen.queryByText("pnl_demo_only_once")).not.toBeInTheDocument();
  });

  it("hides the owner role from a non-owner editor", () => {
    render(
      <MemberRoleSelect
        actorRole="admin"
        value="viewer"
        onChange={() => undefined}
      />,
    );

    expect(
      screen.queryByRole("option", { name: "Owner" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Admin" })).toBeInTheDocument();
  });

  it("copies a tracking snippet containing the site public ID", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    render(<TrackingSnippet publicId="site_public_42" />);

    fireEvent.click(
      screen.getByRole("button", { name: /copy tracking snippet/i }),
    );

    expect(await screen.findByText(/copied/i)).toBeVisible();
    expect(writeText).toHaveBeenCalledWith(
      expect.stringContaining("site_public_42"),
    );
    expect(writeText).toHaveBeenCalledWith(
      expect.stringContaining("/js/script.full.js"),
    );
  });

  it("switches between the server full and core snippets", () => {
    const view = render(
      <TrackingSnippet
        publicId="site_public_42"
        snippet='<script src="https://stats.example/js/custom.js"></script>'
        fullSnippet='<script src="https://stats.example/js/custom.full.js"></script>'
      />,
    );

    const full = view.getByRole("radio", { name: /full script/i });
    const core = view.getByRole("radio", { name: /core script/i });
    expect(full).toBeChecked();
    expect(view.getByText(/custom\.full\.js/)).toBeVisible();
    expect(view.getByText(/web vitals/i)).toBeVisible();

    fireEvent.click(core);
    expect(core).toBeChecked();
    expect(view.getByText(/custom\.js/)).toBeVisible();
  });

  it("renders a TOTP QR locally without a network request", () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    render(
      <TotpQr uri="otpauth://totp/Panel:test@example.com?secret=JBSWY3DPEHPK3PXP&issuer=Panel" />,
    );

    expect(
      screen.getByRole("img", { name: /authenticator setup qr code/i }),
    ).toBeVisible();
    expect(fetchSpy).not.toHaveBeenCalled();
    fetchSpy.mockRestore();
  });
});
