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
    duplicates: 0,
    late_drops: 0,
    clock_skew_clamps: 0,
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
    duplicates: 0,
    late_drops: 0,
    clock_skew_clamps: 0,
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

it("shows collection reliability signals in a distinct section", async () => {
  request.mockResolvedValue({
    ingest_rate: 17,
    drops_503: 2,
    bots_filtered: 11,
    queue_depth: 4,
    close_lag_seconds: 9,
    duplicates: 23,
    late_drops: 5,
    clock_skew_clamps: 7,
    failed_profile_erasures: 0,
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

  const collection = await screen.findByRole("region", { name: "Collection" });
  await screen.findByText("23");
  expect(collection).toHaveTextContent("Duplicates23");
  expect(collection).toHaveTextContent("Late drops5");
  expect(collection).toHaveTextContent("Clock-skew clamps7");
  expect(collection).not.toHaveTextContent("Events rejected");
});

it("shows failed profile erasure requests that need attention", async () => {
  request.mockResolvedValue({
    ingest_rate: 0,
    drops_503: 0,
    bots_filtered: 0,
    queue_depth: 0,
    close_lag_seconds: 0,
    duplicates: 0,
    late_drops: 0,
    clock_skew_clamps: 0,
    failed_profile_erasures: 2,
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

  expect(
    await screen.findByRole("alert"),
  ).toHaveTextContent("2 profile erasure requests need attention.");
});
