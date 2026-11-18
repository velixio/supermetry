import "@testing-library/jest-dom/vitest";

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import { privateRequest } from "@/features/settings/privateApi";
import { ObsIngestCard } from "./ObsIngestCard";

vi.mock("@/features/settings/privateApi", () => ({ privateRequest: vi.fn() }));
const request = vi.mocked(privateRequest);

afterEach(() => { cleanup(); vi.clearAllMocks(); });

const day = (d: string, la = 0, lr = 0, ma = 0, mr = 0) => ({ day: d, logs_accepted: la, logs_rejected: lr, metrics_accepted: ma, metrics_rejected: mr });
const renderCard = () => render(
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    <ObsIngestCard siteId={9} name="Desk app" />
  </QueryClientProvider>,
);

it("lists accepted and rejected logs and metrics per day, newest first", async () => {
  request.mockResolvedValue({ days: [
    day("2026-11-12"), day("2026-11-13", 0, 0, 70, 7), day("2026-11-14"), day("2026-11-15", 40), day("2026-11-16"), day("2026-11-17"), day("2026-11-18", 1200, 5, 900, 0),
  ] });
  renderCard();
  const table = await screen.findByRole("table", { name: "Desk app logs and metrics intake" });
  expect(request).toHaveBeenCalledWith("GET", "/api/private/sites/9/obs/ingest");
  const rows = within(table).getAllByRole("row");
  expect(rows).toHaveLength(8);
  expect(rows[1]).toHaveTextContent("2026-11-18");
  expect(rows[1]).toHaveTextContent("1,200");
  expect(rows[1]).toHaveTextContent("900");
  expect(rows[7]).toHaveTextContent("2026-11-12");
});

it("says so when nothing arrived in 7 days", async () => {
  request.mockResolvedValue({ days: ["2026-11-12", "2026-11-13", "2026-11-14", "2026-11-15", "2026-11-16", "2026-11-17", "2026-11-18"].map((d) => day(d)) });
  renderCard();
  expect(await screen.findByText("No logs or metrics in the last 7 days.")).toBeInTheDocument();
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
});
