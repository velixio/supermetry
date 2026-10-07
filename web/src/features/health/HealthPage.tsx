import { useQuery } from "@tanstack/react-query";

import { fmt } from "@/lib/format";
import { privateRequest } from "@/features/settings/privateApi";

type Health = {
  ingest_rate: number;
  drops_503: number;
  bots_filtered: number;
  queue_depth: number;
  close_lag_seconds: number;
};

const counters: { key: keyof Health; label: string; suffix?: string }[] = [
  { key: "ingest_rate", label: "Events per second" },
  { key: "drops_503", label: "Events rejected" },
  { key: "bots_filtered", label: "Bots filtered" },
  { key: "queue_depth", label: "Queue depth" },
  { key: "close_lag_seconds", label: "Day-close lag", suffix: "s" },
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
      {health.error ? (
        <p role="alert" className="text-sm text-destructive">
          {health.error.message}
        </p>
      ) : null}
    </div>
  );
}
