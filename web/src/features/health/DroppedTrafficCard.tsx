import type { components } from "@/lib/api.gen";
import { fmt } from "@/lib/format";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";

type SiteDropHealth = components["schemas"]["SiteDropHealth"];

export const reasonLabels: Record<string, string> = {
  bot_user_agent: "Bot user agent",
  bot_headless: "Headless browser",
  bot_spam_referrer: "Spam referrer",
  rate_limited_ip: "IP rate limit",
  rate_limited_site: "Site rate limit",
  rate_limited_global: "Global rate limit",
  privacy_signal: "Privacy signal",
  dimension_overflow: "Dimension overflow",
  host_not_allowed: "Host not allowed",
  clock_skew: "Clock skew",
};

export function DroppedTrafficCard({ site }: { site: SiteDropHealth }) {
  const visible = site.reasons.filter((reason) => reason.today > 0 || reason.previous_7_days > 0);
  return (
    <Card>
      <CardHeader>
        <CardTitle>{site.site_public_id || `Site ${site.site_id}`}</CardTitle>
        <CardDescription>Today compared with the previous seven complete local days.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {visible.length ? (
          <div className="flex flex-col gap-2">
            {visible.map((reason) => (
              <div className="grid grid-cols-[minmax(0,1fr)_auto_auto_auto] items-center gap-3 text-sm" key={reason.reason}>
                <span className="truncate">{reasonLabels[reason.reason] ?? reason.reason}</span>
                <span className="font-medium tabular-nums">{fmt.number(reason.today)}</span>
                <span className="text-xs text-muted-foreground">7d {fmt.number(reason.previous_7_days)}</span>
                {reason.spike ? <Badge variant="destructive">Spike</Badge> : <span />}
              </div>
            ))}
          </div>
        ) : <p className="text-sm text-muted-foreground">No dropped traffic in this window.</p>}
        {site.top_bot_rules.length ? (
          <div className="flex flex-col gap-2">
            <Separator />
            <p className="text-xs font-medium text-muted-foreground">Top bot rules today</p>
            {site.top_bot_rules.map((rule) => (
              <div className="flex items-center justify-between gap-3 text-sm" key={rule.rule}>
                <code className="truncate text-xs">{rule.rule}</code>
                <span className="tabular-nums">{fmt.number(rule.count)}</span>
              </div>
            ))}
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}
