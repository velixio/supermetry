import "@testing-library/jest-dom/vitest";

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ApiDocsPage } from "./ApiDocsPage";
import type { OpenAPIDocument } from "./reference";

const specification: OpenAPIDocument = {
  openapi: "3.1.0",
  info: { title: "Panel API", version: "1.0.0" },
  paths: {
    "/api/public/v1/stats/summary": {
      get: {
        operationId: "statsSummary",
        summary: "Get summary statistics",
        tags: ["Statistics"],
        parameters: [
          {
            name: "site_id",
            in: "query",
            required: true,
            schema: { type: "integer" },
          },
          {
            name: "period",
            in: "query",
            required: true,
            schema: { type: "string", example: "30d" },
          },
        ],
        responses: {
          "200": {
            description: "Summary",
            content: {
              "application/json": { example: { totals: { visitors: 42 } } },
            },
          },
        },
      },
    },
  },
};

describe("ApiDocsPage", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("renders grouped operations, code examples, schemas, and a private try form", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify(specification), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ totals: { visitors: 42 } }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
    vi.stubGlobal("fetch", fetchMock);

    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <ApiDocsPage
          orgId={3}
          sites={[
            { id: 7, name: "Docs", slug: "docs" },
            { id: 8, name: "Store", slug: "store" },
          ]}
        />
      </QueryClientProvider>,
    );

    expect(
      await screen.findByRole("heading", { name: "API reference" }),
    ).toBeVisible();
    expect(screen.getByRole("heading", { name: "Statistics" })).toBeVisible();
    expect(screen.getAllByText("GET")).toHaveLength(2);
    expect(screen.getByText("/api/public/v1/stats/summary")).toBeVisible();
    expect(screen.getByRole("tab", { name: "JavaScript" })).toBeVisible();
    expect(
      screen.getByRole("button", { name: /Get summary statistics/ }),
    ).toHaveAttribute("aria-current", "page");

    fireEvent.change(screen.getByLabelText("Period"), {
      target: { value: "7d" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Send request" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    expect(fetchMock.mock.calls[1]?.[0]).toBe(
      "/api/private/stats/summary?site_id=7&period=7d",
    );
    expect(fetchMock.mock.calls[1]?.[1]).toMatchObject({
      credentials: "same-origin",
      headers: expect.objectContaining({ "X-Panel-Org": "3" }),
    });
    expect(await screen.findByLabelText("Response")).toHaveTextContent(
      '"visitors": 42',
    );
  });
});
