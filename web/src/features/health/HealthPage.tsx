import { useQuery } from "@tanstack/react-query";

import { fmt } from "@/lib/format";
import type { components } from "@/lib/api.gen";
import { privateRequest } from "@/features/settings/privateApi";

type Health = components["schemas"]["DataHealthResponse"];
type HealthCounterKey = Exclude<keyof Health, "rollup_backfill">;

const counters: { key: HealthCounterKey; label: string; suffix?: string }[] = [
  { key: "ingest_rate", label: "Events per second" },
  { key: "drops_503", label: "Events rejected" },
  { key: "bots_filtered", label: "Bots filtered" },
  { key: "queue_depth", label: "Queue depth" },
  { key: "close_lag_seconds", label: "Day-close lag", suffix: "s" },
];

const collectionCounters: { key: HealthCounterKey; label: string }[] = [
  { key: "duplicates", label: "Duplicates" },
  { key: "late_drops", label: "Late drops" },
  { key: "clock_skew_clamps", label: "Clock-skew clamps" },
];

export function HealthPage() {
  const health = useQuery({
    queryKey: ["data-health"],
    queryFn: () => privateRequest<Health>("GET", "/api/private/health"),
    refetchInterval: 5_000,
  });
  return (
    <div className="mx-auto flex w-full max-w-5xl flex-col gap-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">Data health</h1>
        <p className="text-sm text-muted-foreground">
          Live intake and aggregation signals, refreshed every five seconds.
        </p>
      </header>
      {health.data?.rollup_backfill.status === "running" ? (
        <p
          role="status"
          className="rounded-xl border border-primary/20 bg-primary/5 px-4 py-3 text-sm"
        >
          Updating historical breakdowns: {health.data.rollup_backfill.days_done} of{" "}
          {health.data.rollup_backfill.total_days} days
        </p>
      ) : null}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {counters.map((counter) => (
          <section className="rounded-xl border bg-card p-5" key={counter.key}>
            <h2 className="text-sm font-medium text-muted-foreground">
              {counter.label}
            </h2>
            <p className="mt-2 text-3xl font-semibold tabular-nums">
              {health.data
                ? `${fmt.number(health.data[counter.key])}${counter.suffix ?? ""}`
                : "—"}
            </p>
          </section>
        ))}
      </div>
      <section aria-labelledby="collection-heading" className="flex flex-col gap-3">
        <div>
          <h2 id="collection-heading" className="text-lg font-semibold">
            Collection
          </h2>
          <p className="text-sm text-muted-foreground">
            Events safely ignored or corrected during intake.
          </p>
        </div>
        <div className="grid gap-4 sm:grid-cols-3">
          {collectionCounters.map((counter) => (
            <div className="rounded-xl border bg-card p-5" key={counter.key}>
              <h3 className="text-sm font-medium text-muted-foreground">
                {counter.label}
              </h3>
              <p className="mt-2 text-3xl font-semibold tabular-nums">
                {health.data ? fmt.number(health.data[counter.key]) : "—"}
              </p>
            </div>
          ))}
        </div>
      </section>
      {health.error ? (
        <p role="alert" className="text-sm text-destructive">
          {health.error.message}
        </p>
      ) : null}
    </div>
  );
}
