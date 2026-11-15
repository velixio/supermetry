import "@testing-library/jest-dom/vitest";

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";

import type { components } from "@/lib/api.gen";
import { DroppedTrafficCard } from "./DroppedTrafficCard";

type SiteDropHealth = components["schemas"]["SiteDropHealth"];

afterEach(cleanup);

function site(reason: string): SiteDropHealth {
  return {
    site_id: 1,
    site_public_id: "pub",
    reasons: [{ reason, today: 4, previous_7_days: 0, trailing_7_day_average: 0, spike: false }],
    top_bot_rules: [],
  } as unknown as SiteDropHealth;
}

it("labels too_old drops", () => {
  render(<DroppedTrafficCard site={site("too_old")} />);
  expect(screen.getByText("Too old")).toBeInTheDocument();
});

it("falls back to the raw name for an unknown reason", () => {
  render(<DroppedTrafficCard site={site("brand_new_reason")} />);
  expect(screen.getByText("brand_new_reason")).toBeInTheDocument();
});
