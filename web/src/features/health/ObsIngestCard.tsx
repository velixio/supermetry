import { useQuery } from "@tanstack/react-query";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { privateRequest } from "@/features/settings/privateApi";
import type { components } from "@/lib/api.gen";
import { fmt } from "@/lib/format";

type ObsIngestHealth = components["schemas"]["ObsIngestHealth"];

/** Logs and metrics accepted and rejected per UTC day for one app project, last 7 days. */
export function ObsIngestCard({ siteId, name }: { siteId: number; name: string }) {
  const ingest = useQuery({
    queryKey: ["obs-ingest", siteId],
    queryFn: () => privateRequest<ObsIngestHealth>("GET", `/api/private/sites/${siteId}/obs/ingest`),
    refetchInterval: 60_000,
  });
  const days = ingest.data?.days ?? [];
  const active = days.some((d) => d.logs_accepted + d.logs_rejected + d.metrics_accepted + d.metrics_rejected > 0);
  return (
    <Card>
      <CardHeader>
        <CardTitle>{name}: logs and metrics</CardTitle>
        <CardDescription>Accepted and rejected per UTC day. Rejected means over the daily quota.</CardDescription>
      </CardHeader>
      <CardContent>
        {ingest.isError ? <p role="alert" className="text-sm text-destructive">{ingest.error.message}</p> : null}
        {ingest.isSuccess && !active ? <p className="text-sm text-muted-foreground">No logs or metrics in the last 7 days.</p> : null}
        {active ? (
          <table className="w-full text-sm" aria-label={`${name} logs and metrics intake`}>
            <thead>
              <tr className="text-left text-xs text-muted-foreground">
                <th scope="col" className="font-normal">Day</th>
                <th scope="col" className="text-right font-normal">Logs accepted</th>
                <th scope="col" className="text-right font-normal">Logs rejected</th>
                <th scope="col" className="text-right font-normal">Metrics accepted</th>
                <th scope="col" className="text-right font-normal">Metrics rejected</th>
              </tr>
            </thead>
            <tbody>
              {[...days].reverse().map((d) => (
                <tr key={d.day}>
                  <th scope="row" className="text-left font-normal">{d.day}</th>
                  <td className="text-right tabular-nums">{fmt.number(d.logs_accepted)}</td>
                  <td className="text-right tabular-nums">{fmt.number(d.logs_rejected)}</td>
                  <td className="text-right tabular-nums">{fmt.number(d.metrics_accepted)}</td>
                  <td className="text-right tabular-nums">{fmt.number(d.metrics_rejected)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : null}
      </CardContent>
    </Card>
  );
}
