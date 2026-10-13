import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { useQuery } from "@tanstack/react-query";

import { fmt } from "@/lib/format";
import type { components } from "@/lib/api.gen";
import { privateRequest } from "@/features/settings/privateApi";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { DroppedTrafficCard, reasonLabels } from "./DroppedTrafficCard";

type Health = components["schemas"]["DataHealthResponse"] & {
  failed_profile_erasures: number;
};
type HealthCounterKey = Exclude<
  keyof Health,
  | "failed_profile_erasures"
  | "rollup_backfill"
  | "site_drops"
  | "instance_scope"
>;

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

export function HealthPage({ orgId, version = "dev" }: { orgId?: number; version?: string } = {}) {
  const health = useQuery({
    queryKey: ["data-health", orgId],
    queryFn: () =>
      privateRequest<Health>("GET", "/api/private/health", { orgId }),
    refetchInterval: 5_000,
  });
  const instanceScope = health.data ? health.data.instance_scope : true;
  return (
    <PageContainer>
      <PageHeader
        title="Data health"
        description={
          <>
            Live intake and aggregation signals, refreshed every five seconds.
            <span className="mt-1 block text-xs">Supermetry {version || "dev"}</span>
          </>
        }
      />
      {instanceScope && health.data?.rollup_backfill.status === "running" ? (
        <p
          role="status"
          className="rounded-xl border border-primary/20 bg-primary/5 px-4 py-3 text-sm"
        >
          Updating historical breakdowns:{" "}
          {health.data.rollup_backfill.days_done} of{" "}
          {health.data.rollup_backfill.total_days} days
        </p>
      ) : null}
      {instanceScope &&
      health.data &&
      health.data.failed_profile_erasures > 0 ? (
        <Alert variant="destructive">
          <AlertTitle>Profile erasure needs attention</AlertTitle>
          <AlertDescription>
            {health.data.failed_profile_erasures} profile erasure{" "}
            {health.data.failed_profile_erasures === 1
              ? "request needs"
              : "requests need"}{" "}
            attention.
          </AlertDescription>
        </Alert>
      ) : null}
      {instanceScope ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {counters.map((counter) => (
            <section
              className="rounded-xl border bg-card p-5"
              key={counter.key}
            >
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
      ) : null}
      {instanceScope ? (
        <section
          aria-labelledby="collection-heading"
          className="flex flex-col gap-3"
        >
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
      ) : null}
      <section
        aria-labelledby="dropped-traffic-heading"
        className="flex flex-col gap-3"
      >
        <div>
          <h2 id="dropped-traffic-heading" className="text-lg font-semibold">
            Dropped and throttled traffic
          </h2>
          <p className="text-sm text-muted-foreground">
            Per-site counts use each site's local day.
          </p>
          <a
            className="text-sm text-primary underline-offset-4 hover:underline"
            href="#drop-reason-explanations"
          >
            Why traffic is dropped
          </a>
        </div>
        <div className="grid gap-4 lg:grid-cols-2">
          {(health.data?.site_drops ?? []).map((site) => (
            <DroppedTrafficCard key={site.site_id} site={site} />
          ))}
        </div>
        <div
          id="drop-reason-explanations"
          className="grid gap-2 text-xs text-muted-foreground sm:grid-cols-2"
        >
          {Object.entries(reasonLabels).map(([reason, label]) => (
            <p key={reason}>
              <span className="font-medium text-foreground">{label}:</span>{" "}
              {reasonExplanation(reason)}
            </p>
          ))}
        </div>
      </section>
      {health.error ? (
        <p role="alert" className="text-sm text-destructive">
          {health.error.message}
        </p>
      ) : null}
    </PageContainer>
  );
}

function reasonExplanation(reason: string): string {
  const explanations: Record<string, string> = {
    bot_user_agent: "matched a known crawler user-agent rule.",
    bot_headless: "matched an automated headless-browser signature.",
    bot_spam_referrer: "used a known referrer-spam domain.",
    rate_limited_ip: "one network identity exceeded its intake allowance.",
    rate_limited_site: "this site exceeded its intake allowance.",
    rate_limited_global: "the instance reached its global intake allowance.",
    privacy_signal: "the browser sent DNT or Global Privacy Control.",
    dimension_overflow: "a high-cardinality value was grouped into Other.",
    host_not_allowed: "the page host was not configured for this site.",
    clock_skew: "a future timestamp was clamped to server time.",
  };
  return explanations[reason] ?? "traffic was not accepted for this reason.";
}
