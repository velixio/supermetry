import { describe, expect, it, vi } from "vitest";

import { createSupermetryClient } from "./index";

describe("createSupermetryClient", () => {
  it("sends the API key as a bearer token", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ data: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const client = createSupermetryClient({
      apiKey: "supermetry_secret",
      baseUrl: "https://analytics.supermetry.com",
      fetch: fetchMock,
    });

    await client.GET("/api/public/v1/sites");

    const request = fetchMock.mock.calls[0]?.[0] as Request;
    expect(request.url).toBe(
      "https://analytics.supermetry.com/api/public/v1/sites",
    );
    expect(request.headers.get("Authorization")).toBe("Bearer supermetry_secret");
  });

  it("defaults to Supermetry Cloud", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("{}", { status: 200 }));
    const client = createSupermetryClient({ apiKey: "supermetry_secret", fetch: fetchMock });

    await client.GET("/api/public/v1/sites");

    const request = fetchMock.mock.calls[0]?.[0] as Request;
    expect(request.url).toBe("https://app.supermetry.com/api/public/v1/sites");
  });
});
