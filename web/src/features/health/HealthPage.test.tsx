import "@testing-library/jest-dom/vitest";

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import { HealthPage } from "./HealthPage";
import { privateRequest } from "@/features/settings/privateApi";

vi.mock("@/features/settings/privateApi", () => ({
  privateRequest: vi.fn(),
}));

const request = vi.mocked(privateRequest);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

it("shows historical breakdown backfill progress", async () => {
  request.mockResolvedValue({
    ingest_rate: 0,
    drops_503: 0,
    bots_filtered: 0,
    queue_depth: 0,
    close_lag_seconds: 0,
    rollup_backfill: { status: "running", days_done: 37, total_days: 120 },
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={client}>
      <HealthPage />
    </QueryClientProvider>,
  );

  expect(
    await screen.findByText("Updating historical breakdowns: 37 of 120 days"),
  ).toBeVisible();
});

it("hides historical breakdown progress after completion", async () => {
  request.mockResolvedValue({
    ingest_rate: 0,
    drops_503: 0,
    bots_filtered: 0,
    queue_depth: 0,
    close_lag_seconds: 0,
    rollup_backfill: { status: "finished", days_done: 120, total_days: 120 },
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={client}>
      <HealthPage />
    </QueryClientProvider>,
  );

  expect(await screen.findByText("Data health")).toBeVisible();
  expect(screen.queryByText(/Updating historical breakdowns/)).not.toBeInTheDocument();
});
