import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import type { TrackingPolicy } from "@/features/sites/types";
import { useState, type FormEvent, type ReactNode } from "react";
import { copyText } from "@/lib/clipboard";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ExternalLinkIcon, KeyRoundIcon, ShieldCheckIcon } from "lucide-react";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { fmt } from "@/lib/format";
import { en } from "@/messages/en";

import { ApiKeySecretDialog } from "./ApiKeySecretDialog";
import { type MemberRole } from "./MemberRoleSelect";
import { MemberAccessControls, siteAccessLabel, type MemberAccess } from "./MemberAccessControls";
import { privateRequest, type ListResponse } from "./privateApi";
import { TotpQr } from "./TotpQr";
import { InstallGuide, SiteIconSettings, installationStatusInterval } from "./SiteSettings";
import { ProfilesSettings } from "./ProfilesSettings";
import type { Role } from "@/components/Sidebar";
import { activeLocale } from '@/lib/i18n/intl'

type Site = {
  id: number;
  public_id: string;
  name: string;
  allowed_hosts: string[];
  timezone: string;
  retention_days: number;
  profiles_enabled: boolean;
  tracking_policy?: TrackingPolicy;
  install_status?: "waiting" | "receiving" | "quiet";
  last_seen_at?: string | null;
  icon_url?: string;
  revenue_currency?: string;
  revenue_currency_state: "unconfigured" | "configured" | "conflict";
};

type Member = {
  user_id: number;
  email: string;
  name: string;
  role: MemberRole;
  site_ids: number[] | null;
  capabilities: string[];
  billing_admin: boolean;
};
type Invitation = {
  id: number;
  email: string;
  role: MemberRole;
  expires_at: string;
  revoked_at?: string;
  site_ids: number[] | null;
  capabilities: string[];
  billing_admin: boolean;
};
type ApiKey = {
  id?: number;
  ID?: number;
  name?: string;
  Name?: string;
  prefix?: string;
  Prefix?: string;
  scopes?: string[];
  Scopes?: string[];
  site_ids?: number[] | null;
  SiteIDs?: number[] | null;
  created_at?: string;
  CreatedAt?: string;
  expires_at?: string | null;
  ExpiresAt?: string | null;
  last_used_at?: string | null;
  LastUsedAt?: string | null;
  revoked?: boolean;
  Revoked?: boolean;
  revoked_at?: string | null;
  RevokedAt?: string;
};
type Share = {
  id: number;
  password_protected: boolean;
  expires_at?: string | null;
  revoked_at?: string;
  config?: ShareConfig;
};
type ShareSection = "kpis" | "chart" | "pages" | "sources" | "locations" | "devices" | "where" | "weekly_rhythm" | "goals";
type ShareConfig = {
  sections: ShareSection[];
  filters: { dimension: string; operator: "is"; values: string[] }[];
  segment_id?: number;
  date: { mode: "rolling" | "fixed"; days?: number; from?: string; to?: string; max_days: number };
  theme: "light" | "dark" | "system";
  embed: { enabled: boolean; allowed_origins: string[]; confirm_wildcard: boolean };
};
type SavedSegment = { id: number; name: string };
const shareSectionOptions: { value: ShareSection; label: string }[] = [
  { value: "kpis", label: "KPIs" }, { value: "chart", label: "Chart" },
  { value: "pages", label: "Pages" }, { value: "sources", label: "Sources" },
  { value: "locations", label: "Locations" }, { value: "devices", label: "Devices" },
  { value: "where", label: "Where" }, { value: "weekly_rhythm", label: "Weekly rhythm" },
  { value: "goals", label: "Goals" },
];
type Report = { id: number; cadence: string; last_sent_at?: string };
type AuditEntry = {
  id: number;
  action: string;
  target_type: string;
  target_id: number;
  created_at: string;
};

function Page({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: ReactNode;
}) {
  return (
    <PageContainer width="narrow">
      <PageHeader title={title} description={description} />
      {children}
    </PageContainer>
  );
}

function Surface({ children }: { children: ReactNode }) {
  return (
    <section className="flex flex-col gap-5 rounded-xl border bg-card p-5 text-card-foreground">
      {children}
    </section>
  );
}

function Message({ error }: { error: Error | null }) {
  return error ? (
    <p role="alert" className="text-sm text-destructive">
      {error.message}
    </p>
  ) : null;
}

export function SiteGeneralPage({
  siteId,
  orgId,
  role,
}: {
  siteId: number;
  orgId: number;
  role: Role;
}) {
  const queryClient = useQueryClient();
  const site = useQuery({
    queryKey: ["site", siteId],
    queryFn: () =>
      privateRequest<Site>("GET", `/api/private/sites/${siteId}`, { orgId }),
  });
  const save = useMutation({
    mutationFn: (body: Record<string, unknown>) =>
      privateRequest<Site>("PATCH", `/api/private/sites/${siteId}`, {
        orgId,
        body,
      }),
    onSuccess: (value) => queryClient.setQueryData(["site", siteId], value),
  });
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const revenueCurrency = String(data.get("revenue_currency") ?? "")
      .trim()
      .toUpperCase();
    save.mutate({
      name: String(data.get("name") ?? ""),
      hosts: String(data.get("hosts") ?? "")
        .split(",")
        .map((host) => host.trim())
        .filter(Boolean),
      timezone: String(data.get("timezone") ?? ""),
      retention_days: Number(data.get("retention")),
      ...(revenueCurrency ? { revenue_currency: revenueCurrency } : {}),
    });
  }
  return (
    <Page
      title="Site settings"
      description="Control identity, accepted hosts, local time, and raw-data retention."
    >
      <Surface>
        {site.data ? (
          <>
            <SiteIconSettings
              iconUrl={site.data.icon_url}
              name={site.data.name}
              orgId={orgId}
              role={role}
              siteId={siteId}
            />
            <form className="flex flex-col gap-5" onSubmit={submit}>
              <FieldGroup>
                <Field>
                  <FieldLabel htmlFor="site-name">Name</FieldLabel>
                  <Input
                    id="site-name"
                    name="name"
                    defaultValue={site.data.name}
                  />
                </Field>
                <RevenueCurrencySettings site={site.data} role={role} />
                <Field>
                  <FieldLabel htmlFor="site-hosts">Allowed hosts</FieldLabel>
                  <Input
                    id="site-hosts"
                    name="hosts"
                    defaultValue={site.data.allowed_hosts.join(", ")}
                  />
                  <FieldDescription>
                    Separate multiple hosts with commas.
                  </FieldDescription>
                </Field>
                <Field>
                  <FieldLabel htmlFor="site-timezone">Time zone</FieldLabel>
                  <Input
                    id="site-timezone"
                    name="timezone"
                    defaultValue={site.data.timezone}
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor="site-retention">Retention</FieldLabel>
                  <select
                    id="site-retention"
                    name="retention"
                    defaultValue={site.data.retention_days}
                    className="h-9 rounded-lg border bg-background px-3 text-sm"
                  >
                    <option value="7">7 days</option>
                    <option value="30">30 days</option>
                    <option value="90">90 days</option>
                    <option value="365">365 days</option>
                    <option value="0">Forever</option>
                  </select>
                </Field>
                <ProfilesSettings
                  enabled={site.data.profiles_enabled}
                  policy={site.data.tracking_policy}
                  role={role}
                  onChange={(profiles_enabled) => save.mutate({ profiles_enabled })}
                />
              </FieldGroup>
              <Message error={save.error} />
              <Button
                className="self-start"
                disabled={save.isPending}
                type="submit"
              >
                Save changes
              </Button>
            </form>
          </>
        ) : (
          <p className="text-sm text-muted-foreground">
            Loading site settings…
          </p>
        )}
      </Surface>
    </Page>
  );
}

export function RevenueCurrencySettings({ site, role }: { site: Site; role: Role }) {
  if (site.revenue_currency_state === "conflict") {
    return (
      <div role="alert" className="rounded-lg border border-destructive/40 p-3 text-sm">
        <strong>Revenue reporting paused.</strong> This project contains mixed historical currencies.
        Ordinary analytics remain available; resolve the historical data before tracking or totaling revenue.
      </div>
    );
  }
  if (site.revenue_currency_state === "configured") {
    return (
      <Field>
        <FieldLabel htmlFor="site-revenue-currency">Revenue currency</FieldLabel>
        <Input id="site-revenue-currency" value={site.revenue_currency ?? ""} readOnly />
        <FieldDescription>This currency is permanent for the project.</FieldDescription>
      </Field>
    );
  }
  const allowed = role === "owner" || role === "admin";
  return (
    <Field>
      <FieldLabel htmlFor="site-revenue-currency">Revenue currency</FieldLabel>
      <Input
        id="site-revenue-currency"
        name="revenue_currency"
        maxLength={3}
        pattern="[A-Za-z]{3}"
        placeholder="USD"
        disabled={!allowed}
        required={allowed}
      />
      <FieldDescription>
        {allowed
          ? "Choose the three-letter currency for all project revenue. This choice is permanent."
          : "An owner or admin must choose the permanent project currency."}
      </FieldDescription>
    </Field>
  );
}

export function InstallationPage({
  siteId,
  publicId,
  orgId,
  isDemo = false,
}: {
  siteId: number;
  publicId: string;
  orgId: number;
  isDemo?: boolean;
}) {
  const status = useQuery({
    queryKey: ["tracking-status", siteId],
    queryFn: () =>
      privateRequest<Site>("GET", `/api/private/sites/${siteId}`, { orgId }),
    refetchInterval: installationStatusInterval,
    enabled: !isDemo,
  });
  const snippet = useQuery({
    queryKey: ["site-snippet", orgId, siteId],
    queryFn: () =>
      privateRequest<{ snippet: string; full_snippet: string }>("GET", `/api/private/sites/${siteId}/snippet`, { orgId }),
    enabled: !isDemo,
  });
  return (
    <Page
      title="Installation"
      description="Install the tracker once, then verify the first event arrives."
    >
      <Surface>
        {isDemo ? (
          <Alert>
            <AlertTitle>Demo project</AlertTitle>
            <AlertDescription>Demo projects don't accept real traffic.</AlertDescription>
          </Alert>
        ) : <InstallGuide
          publicId={publicId}
          snippet={snippet.data?.snippet}
          fullSnippet={snippet.data?.full_snippet}
          installStatus={status.data?.install_status}
          lastSeenAt={status.data?.last_seen_at}
          revenueCurrency={status.data?.revenue_currency}
          verify={async () => {
            const result = await status.refetch();
            return { status: result.data?.install_status, lastSeenAt: result.data?.last_seen_at };
          }}
        />}
      </Surface>
    </Page>
  );
}

export function SiteLinksPage({
  org,
  site,
  kind,
}: {
  org: string;
  site: string;
  kind: "goals" | "funnels";
}) {
  const label = kind === "goals" ? "Goals" : "Funnels";
  return (
    <Page
      title={`${label} settings`}
      description={`Create and manage ${kind} from their dedicated workspace.`}
    >
      <Surface>
        <Button asChild className="self-start">
          <Link
            to={kind === "goals" ? "/app/$org/$site/goals" : "/app/$org/$site/funnels"}
            params={{ org, site }}
          >
            {`Open ${kind}`}
            <ExternalLinkIcon data-icon="inline-end" />
          </Link>
        </Button>
      </Surface>
    </Page>
  );
}

export function SharesPage({
  siteId,
  orgId,
}: {
  siteId: number;
  orgId: number;
}) {
  const queryClient = useQueryClient();
  const [createdLink, setCreatedLink] = useState("");
  const [expiresInDays, setExpiresInDays] = useState<number | null>(90);
  const [sections, setSections] = useState<ShareSection[]>(
    shareSectionOptions.map(({ value }) => value),
  );
  const [theme, setTheme] = useState<ShareConfig["theme"]>("system");
  const [dateMode, setDateMode] = useState<"rolling" | "fixed">("rolling");
  const [rollingDays, setRollingDays] = useState(30);
  const [maxDays, setMaxDays] = useState(30);
  const [fixedFrom, setFixedFrom] = useState("");
  const [fixedTo, setFixedTo] = useState("");
  const [country, setCountry] = useState("");
  const [segmentId, setSegmentId] = useState<number | null>(null);
  const [embedding, setEmbedding] = useState(false);
  const [allowedOrigins, setAllowedOrigins] = useState("");
  const [confirmWildcard, setConfirmWildcard] = useState(false);
  const key = ["shares", siteId];
  const shares = useQuery({
    queryKey: key,
    queryFn: () =>
      privateRequest<ListResponse<Share>>(
        "GET",
        `/api/private/sites/${siteId}/shares`,
        { orgId },
      ),
  });
  const segments = useQuery({
    queryKey: ["segments", siteId, "share-composer"],
    queryFn: () =>
      privateRequest<SavedSegment[]>(
        "GET",
        `/api/private/sites/${siteId}/segments`,
        { orgId },
      ),
  });
  const create = useMutation({
    mutationFn: ({ password, expiresInDays, config }: { password: string; expiresInDays: number | null; config: ShareConfig }) =>
      privateRequest<{ slug: string }>(
        "POST",
        `/api/private/sites/${siteId}/shares`,
        { orgId, body: { password, expires_in_days: expiresInDays, config } },
      ),
    onSuccess: (data) => {
      setCreatedLink(`${window.location.origin}/share/${data.slug}`);
      void queryClient.invalidateQueries({ queryKey: key });
    },
  });
  const revoke = useMutation({
    mutationFn: (id: number) =>
      privateRequest<void>(
        "DELETE",
        `/api/private/sites/${siteId}/shares/${id}`,
        { orgId },
      ),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: key }),
  });
  const extend = useMutation({
    mutationFn: (id: number) =>
      privateRequest<void>(
        "PATCH",
        `/api/private/sites/${siteId}/shares/${id}`,
        { orgId, body: { expires_in_days: 90 } },
      ),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: key }),
  });
  const origins = allowedOrigins
    .split(/[\n,]/)
    .map((origin) => origin.trim())
    .filter(Boolean);
  const canCreate = sections.length > 0 && (!embedding || origins.length > 0 || confirmWildcard);
  return (
    <Page
      title="Shared dashboards"
      description="Create revocable read-only links, with an optional password."
    >
      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(18rem,0.75fr)]">
      <Surface>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            const date = dateMode === "rolling"
              ? { mode: "rolling" as const, days: rollingDays, max_days: maxDays }
              : { mode: "fixed" as const, from: fixedFrom, to: fixedTo, max_days: maxDays };
            create.mutate({
              password: String(new FormData(event.currentTarget).get("password") ?? ""),
              expiresInDays,
              config: {
                sections,
                filters: segmentId === null && country.trim()
                  ? [{ dimension: "country", operator: "is", values: [country.trim().toUpperCase()] }]
                  : [],
                ...(segmentId === null ? {} : { segment_id: segmentId }),
                date,
                theme,
                embed: { enabled: embedding, allowed_origins: origins, confirm_wildcard: confirmWildcard },
              },
            });
          }}
        >
		  <FieldGroup>
			<FieldSet>
			  <FieldLegend>{en.shares.content}</FieldLegend>
			  <FieldDescription>Choose exactly what visitors can query and see.</FieldDescription>
			  <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
				{shareSectionOptions.map((section) => (
				  <label className="flex min-h-11 items-center gap-2 rounded-md border px-3 text-sm" key={section.value}>
					<Checkbox
					  checked={sections.includes(section.value)}
					  onCheckedChange={(checked) => setSections((current) => checked
						? [...current, section.value]
						: current.filter((value) => value !== section.value))}
					/>
					{section.label}
				  </label>
				))}
			  </div>
			</FieldSet>
			<div className="grid gap-4 sm:grid-cols-2">
			  <Field>
				<FieldLabel htmlFor="share-theme">Theme</FieldLabel>
				<Select value={theme} onValueChange={(value) => setTheme(value as ShareConfig["theme"])}>
				  <SelectTrigger id="share-theme" className="h-11"><SelectValue /></SelectTrigger>
				  <SelectContent><SelectGroup>
					<SelectItem value="system">System</SelectItem>
					<SelectItem value="light">Light</SelectItem>
					<SelectItem value="dark">Dark</SelectItem>
				  </SelectGroup></SelectContent>
				</Select>
			  </Field>
			  <Field>
				<FieldLabel htmlFor="share-date-mode">Date range</FieldLabel>
				<Select value={dateMode} onValueChange={(value) => setDateMode(value as "rolling" | "fixed")}>
				  <SelectTrigger id="share-date-mode" className="h-11"><SelectValue /></SelectTrigger>
				  <SelectContent><SelectGroup>
					<SelectItem value="rolling">Rolling</SelectItem>
					<SelectItem value="fixed">Fixed</SelectItem>
				  </SelectGroup></SelectContent>
				</Select>
			  </Field>
			</div>
			{dateMode === "rolling" ? (
			  <Field>
				<FieldLabel htmlFor="share-days">Rolling days</FieldLabel>
				<Input id="share-days" className="h-11" type="number" min={1} max={366} value={rollingDays} onChange={(event) => setRollingDays(Number(event.target.value))} />
			  </Field>
			) : (
			  <div className="grid gap-4 sm:grid-cols-2">
				<Field><FieldLabel htmlFor="share-from">From</FieldLabel><Input id="share-from" className="h-11" type="date" required value={fixedFrom} onChange={(event) => setFixedFrom(event.target.value)} /></Field>
				<Field><FieldLabel htmlFor="share-to">To</FieldLabel><Input id="share-to" className="h-11" type="date" required value={fixedTo} onChange={(event) => setFixedTo(event.target.value)} /></Field>
			  </div>
			)}
			<Field>
			  <FieldLabel htmlFor="share-max-days">Maximum range (days)</FieldLabel>
			  <Input id="share-max-days" className="h-11" type="number" min={1} max={366} value={maxDays} onChange={(event) => setMaxDays(Number(event.target.value))} />
			  <FieldDescription>The server rejects any range wider than this limit.</FieldDescription>
			</Field>
			<Field>
			  <FieldLabel htmlFor="share-country">Locked country</FieldLabel>
			  <Input id="share-country" className="h-11" maxLength={2} disabled={segmentId !== null} placeholder="Optional, for example IN" value={country} onChange={(event) => setCountry(event.target.value)} />
			  <FieldDescription>Visitors cannot remove or replace this filter.</FieldDescription>
			</Field>
			<Field>
			  <FieldLabel htmlFor="share-segment">Saved segment</FieldLabel>
			  <Select value={segmentId === null ? "none" : String(segmentId)} onValueChange={(value) => { setSegmentId(value === "none" ? null : Number(value)); if (value !== "none") setCountry(""); }}>
				<SelectTrigger id="share-segment" className="h-11"><SelectValue /></SelectTrigger>
				<SelectContent><SelectGroup>
				  <SelectItem value="none">No saved segment</SelectItem>
				  {(Array.isArray(segments.data) ? segments.data : []).map((segment) => <SelectItem value={String(segment.id)} key={segment.id}>{segment.name}</SelectItem>)}
				</SelectGroup></SelectContent>
			  </Select>
			  <FieldDescription>The segment is snapshotted when the link is created.</FieldDescription>
			</Field>
			<Field className="rounded-lg border p-4">
			  <div className="flex min-h-11 items-center justify-between gap-4">
				<div><FieldLabel htmlFor="share-embed">Allow embedding</FieldLabel><FieldDescription>Opt in to framing this dashboard.</FieldDescription></div>
				<Switch id="share-embed" aria-label="Allow embedding" checked={embedding} onCheckedChange={setEmbedding} />
			  </div>
			  {embedding ? (
				<div className="mt-3 flex flex-col gap-3">
				  <FieldLabel htmlFor="share-origins">Allowed origins</FieldLabel>
				  <Textarea id="share-origins" placeholder="https://status.example.com" value={allowedOrigins} onChange={(event) => setAllowedOrigins(event.target.value)} />
				  <label className="flex items-start gap-2 text-sm"><Checkbox checked={confirmWildcard} onCheckedChange={(checked) => setConfirmWildcard(checked === true)} />Allow embedding on any origin</label>
				  {origins.length === 0 && confirmWildcard ? <Alert><AlertTitle>Public embedding</AlertTitle><AlertDescription>{en.shares.wildcardWarning}</AlertDescription></Alert> : null}
				</div>
			  ) : null}
			</Field>
			<Field>
			  <FieldLabel htmlFor="share-password">Optional share password</FieldLabel>
			  <Input id="share-password" name="password" type="password" minLength={6} placeholder="Optional password" />
			  <FieldDescription>{en.auth.passwordPolicy}</FieldDescription>
			</Field>
			<Field>
			  <FieldLabel htmlFor="share-expiry">Expiry</FieldLabel>
			  <Select
			    value={expiresInDays === null ? "never" : String(expiresInDays)}
			    onValueChange={(value) => setExpiresInDays(value === "never" ? null : Number(value))}
			  >
			    <SelectTrigger id="share-expiry">
			      <SelectValue />
			    </SelectTrigger>
			    <SelectContent>
			      <SelectGroup>
			        <SelectItem value="never">Never</SelectItem>
			        <SelectItem value="7">7 days</SelectItem>
			        <SelectItem value="30">30 days</SelectItem>
			        <SelectItem value="90">90 days</SelectItem>
			        <SelectItem value="365">365 days</SelectItem>
			      </SelectGroup>
			    </SelectContent>
			  </Select>
			</Field>
			<Button type="submit" disabled={create.isPending || !canCreate}>Create link</Button>
			<Message error={create.error} />
		  </FieldGroup>
        </form>
        {createdLink ? (
          <div className="flex flex-wrap items-center gap-2">
            <code className="break-all text-xs">{createdLink}</code>
            <Button
              variant="outline"
              onClick={() => void copyText(createdLink)}
            >
              Copy link
            </Button>
            {embedding ? (
              <Button
                variant="outline"
                onClick={() => void copyText(`<iframe src="${createdLink}" loading="lazy" referrerpolicy="no-referrer" style="width:100%;height:640px;border:0" title="Shared analytics dashboard"></iframe>`)}
              >
                Copy embed code
              </Button>
            ) : null}
          </div>
        ) : null}
        <ul className="flex flex-col gap-2">
          {shares.data?.data.map((share) => (
            <li
              className="flex items-center justify-between gap-3 border-t pt-3"
              key={share.id}
            >
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-sm">
                  Link {share.id}
                  {share.password_protected ? " · Password protected" : ""}
                </span>
                {share.config ? <Badge variant="outline">{share.config.sections.length} sections · {share.config.theme}</Badge> : null}
                {share.expires_at ? (
                  new Date(share.expires_at).getTime() <= Date.now() ? (
                    <Badge variant="destructive">Expired</Badge>
                  ) : (
                    <Badge variant="secondary">Expires {fmt.date(share.expires_at, Intl.DateTimeFormat().resolvedOptions().timeZone)}</Badge>
                  )
                ) : (
                  <Badge variant="outline">Never expires</Badge>
                )}
              </div>
              <div className="flex gap-2">
                {share.expires_at && !share.revoked_at ? (
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={extend.isPending}
                    onClick={() => extend.mutate(share.id)}
                  >
                    Extend 90 days
                  </Button>
                ) : null}
                <Button
                  size="sm"
                  variant="outline"
                  disabled={Boolean(share.revoked_at) || revoke.isPending}
                  onClick={() => revoke.mutate(share.id)}
                >
                  Revoke
                </Button>
              </div>
            </li>
          ))}
        </ul>
      </Surface>
      <aside className="h-fit rounded-xl border bg-muted/30 p-5 lg:sticky lg:top-6">
        <h2 className="text-lg font-semibold">{en.shares.preview}</h2>
        <p className="mt-1 text-sm text-muted-foreground">Read-only · {dateMode === "rolling" ? `Last ${rollingDays} days` : `${fixedFrom || "From"} to ${fixedTo || "To"}`}</p>
        <div className="mt-4 flex flex-wrap gap-2"><Badge variant="secondary">{theme} theme</Badge>{country ? <Badge variant="secondary">Country: {country.toUpperCase()}</Badge> : null}{segmentId !== null ? <Badge variant="secondary">Saved segment</Badge> : null}{embedding ? <Badge variant="secondary">Embeddable</Badge> : null}</div>
        <ul className="mt-5 grid gap-2">
          {shareSectionOptions.filter(({ value }) => sections.includes(value)).map(({ value, label }) => <li className="rounded-md border bg-background px-3 py-2 text-sm" key={value}>{label}</li>)}
        </ul>
        {sections.length === 0 ? <p className="mt-4 text-sm text-destructive">Select at least one section.</p> : null}
      </aside>
      </div>
    </Page>
  );
}

export function ReportsPage({
  siteId,
  orgId,
}: {
  siteId: number;
  orgId: number;
}) {
  const queryClient = useQueryClient();
  const key = ["reports", siteId];
  const reports = useQuery({
    queryKey: key,
    queryFn: () =>
      privateRequest<ListResponse<Report>>(
        "GET",
        `/api/private/sites/${siteId}/reports`,
        { orgId },
      ),
  });
  const subscribe = useMutation({
    mutationFn: (cadence: string) =>
      privateRequest<void>("POST", `/api/private/sites/${siteId}/reports`, {
        orgId,
        body: { cadence },
      }),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: key }),
  });
  const remove = useMutation({
    mutationFn: (id: number) =>
      privateRequest<void>(
        "DELETE",
        `/api/private/sites/${siteId}/reports/${id}`,
        { orgId },
      ),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: key }),
  });
  return (
    <Page
      title="Email reports"
      description="Receive a concise analytics summary in the site's time zone."
    >
      <Surface>
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => subscribe.mutate("weekly")}>
            Subscribe weekly
          </Button>
          <Button variant="outline" onClick={() => subscribe.mutate("monthly")}>
            Subscribe monthly
          </Button>
        </div>
        <ul className="flex flex-col gap-2">
          {reports.data?.data.map((report) => (
            <li
              className="flex items-center justify-between border-t pt-3"
              key={report.id}
            >
              <span className="text-sm capitalize">
                {report.cadence}
                {report.last_sent_at
                  ? ` · Last sent ${fmt.date(report.last_sent_at, "UTC")}`
                  : ""}
              </span>
              <Button
                size="sm"
                variant="outline"
                onClick={() => remove.mutate(report.id)}
              >
                Unsubscribe
              </Button>
            </li>
          ))}
        </ul>
      </Surface>
    </Page>
  );
}

export function MembersPage({
  orgId,
  actorRole,
  sites,
}: {
  orgId: number;
  actorRole: MemberRole;
  sites: { id: number; name: string }[];
}) {
  const queryClient = useQueryClient();
  const key = ["members", orgId];
  const members = useQuery({
    queryKey: key,
    enabled: actorRole === "owner" || actorRole === "admin",
    queryFn: () =>
      privateRequest<ListResponse<Member>>(
        "GET",
        `/api/private/orgs/${orgId}/members`,
        { orgId },
      ),
  });
  const update = useMutation({
    mutationFn: ({
      member,
      changes,
    }: {
      member: number;
      changes: Partial<MemberAccess>;
    }) =>
      privateRequest<void>(
        "PATCH",
        `/api/private/orgs/${orgId}/members/${member}`,
        { orgId, body: changes },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: key });
      void queryClient.invalidateQueries({ queryKey: ["me"] });
    },
  });
  if (actorRole !== "owner" && actorRole !== "admin")
    return (
      <p role="alert">
        Only organization owners and admins can manage members.
      </p>
    );
  return (
    <Page
      title="Members"
      description="Assign roles, project access, and sensitive data permissions."
    >
      <Surface>
        <Message error={members.error || update.error} />
        <ul className="flex flex-col gap-6">
          {members.data?.data.map((member) => {
            const locked = member.role === "owner" && actorRole !== "owner";
            return (
              <li
                className="flex flex-col gap-4 border-b pb-6"
                key={member.user_id}
              >
                <div>
                  <p className="text-sm font-medium">{member.name}</p>
                  <p className="text-xs text-muted-foreground">
                    {member.email}
                  </p>
                </div>
                <MemberAccessControls
                  value={{
                    role: member.role,
                    site_ids: member.site_ids ?? null,
                    capabilities: member.capabilities ?? [],
                  }}
                  actorRole={actorRole}
                  sites={sites}
                  disabled={locked || update.isPending}
                  onChange={(changes) =>
                    update.mutate({ member: member.user_id, changes })
                  }
                />
              </li>
            );
          })}
        </ul>
      </Surface>
    </Page>
  );
}

export function InvitationsPage({
  orgId,
  actorRole = "viewer",
  sites = [],
}: {
  orgId: number;
  actorRole?: MemberRole;
  sites?: { id: number; name: string }[];
}) {
  const queryClient = useQueryClient();
  const [link, setLink] = useState("");
  const [access, setAccess] = useState<MemberAccess>({
    role: "viewer",
    site_ids: null,
    capabilities: [],
  });
  const canManage = actorRole === "owner" || actorRole === "admin";
  const key = ["invitations", orgId];
  const invitations = useQuery({
    queryKey: key,
    queryFn: () =>
      privateRequest<ListResponse<Invitation>>(
        "GET",
        `/api/private/orgs/${orgId}/invitations`,
        { orgId },
      ),
    enabled: canManage,
  });
  const create = useMutation({
    mutationFn: (email: string) =>
      privateRequest<{ link: string }>(
        "POST",
        `/api/private/orgs/${orgId}/invitations`,
        {
          orgId,
          body: {
            email,
            role: access.role,
            site_ids: access.site_ids,
            capabilities: access.capabilities,
          },
        },
      ),
    onSuccess: (data) => {
      setLink(data.link);
      void queryClient.invalidateQueries({ queryKey: key });
    },
  });
  const revoke = useMutation({
    mutationFn: (id: number) =>
      privateRequest<void>(
        "DELETE",
        `/api/private/orgs/${orgId}/invitations/${id}`,
        { orgId },
      ),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: key }),
  });
  if (!canManage)
    return (
      <p role="alert">
        Only organization owners and admins can manage invitations.
      </p>
    );
  return (
    <Page
      title="Invitations"
      description="Invite a teammate with the project access and permissions they need."
    >
      <Surface>
        <form
          className="flex flex-col gap-5"
          onSubmit={(event) => {
            event.preventDefault();
            create.mutate(
              String(new FormData(event.currentTarget).get("email") ?? ""),
            );
          }}
        >
          <label className="flex flex-col gap-1 text-sm">
            Email
            <Input
              name="email"
              type="email"
              placeholder="teammate@example.com"
              required
            />
          </label>
          <MemberAccessControls
            allowOwner={false}
            value={access}
            actorRole={actorRole}
            sites={sites}
            disabled={create.isPending}
            onChange={(changes) =>
              setAccess((current) => ({ ...current, ...changes }))
            }
          />
          <Button
            className="self-start"
            type="submit"
            disabled={create.isPending}
          >
            Invite
          </Button>
        </form>
        <Message error={invitations.error || create.error || revoke.error} />
        {link ? (
          <Button
            className="self-start"
            variant="outline"
            onClick={() => void copyText(link)}
          >
            Copy invitation link
          </Button>
        ) : null}
        <ul className="flex flex-col gap-2">
          {invitations.data?.data.map((invitation) => (
            <li
              className="flex items-center justify-between gap-3 border-t pt-3"
              key={invitation.id}
            >
              <div className="text-sm">
                <p>
                  {invitation.email} · {invitation.role}
                </p>
                <p className="text-xs text-muted-foreground">
                  {siteAccessLabel(invitation.site_ids)}
                  {invitation.capabilities?.length
                    ? ` · ${invitation.capabilities.join(", ")}`
                    : ""}
                </p>
              </div>
              <Button
                size="sm"
                variant="outline"
                disabled={Boolean(invitation.revoked_at) || revoke.isPending}
                onClick={() => revoke.mutate(invitation.id)}
              >
                Revoke
              </Button>
            </li>
          ))}
        </ul>
      </Surface>
    </Page>
  );
}

const apiKeyScopes = [
  "stats:read",
  "profiles:read",
  "replay:read",
  "data:export",
  "sites:write",
  "goals:write",
  "segments:write",
  "annotations:write",
  "keys:manage",
] as const;

type ApiKeySite = Pick<Site, "id" | "name">;

type ApiKeysPageProps = {
  orgId: number;
  orgSlug: string;
  role: Role;
  sites: ApiKeySite[];
};

export function ApiKeysPage({ orgId, orgSlug, role, sites }: ApiKeysPageProps) {
  if (role !== "owner" && role !== "admin") {
    return (
      <p role="alert" className="text-sm text-muted-foreground">
        {en.apiKeys.unauthorized}
      </p>
    );
  }
  return <ApiKeysManager orgId={orgId} orgSlug={orgSlug} sites={sites} />;
}

type CreateKeyInput = {
  name: string;
  scopes: string[];
  site_ids: number[] | null;
  expires_at?: string;
};

function apiKeyValue<T>(key: ApiKey, camel: keyof ApiKey, pascal: keyof ApiKey): T | undefined {
  return (key[camel] ?? key[pascal]) as T | undefined;
}

function apiKeyStatus(key: ApiKey, now = Date.now()): "active" | "expired" | "revoked" {
  const revoked = key.revoked ?? key.Revoked ?? Boolean(key.revoked_at ?? key.RevokedAt);
  if (revoked) return "revoked";
  const expiresAt = apiKeyValue<string | null>(key, "expires_at", "ExpiresAt");
  return expiresAt && new Date(expiresAt).getTime() <= now ? "expired" : "active";
}

function relativeTime(value: string): string {
  const seconds = Math.round((new Date(value).getTime() - Date.now()) / 1_000);
  const absolute = Math.abs(seconds);
  if (absolute < 60) return relativeTimeFormatter.format(seconds, "second");
  const minutes = Math.round(seconds / 60);
  if (Math.abs(minutes) < 60) return relativeTimeFormatter.format(minutes, "minute");
  const hours = Math.round(minutes / 60);
  if (Math.abs(hours) < 24) return relativeTimeFormatter.format(hours, "hour");
  return relativeTimeFormatter.format(Math.round(hours / 24), "day");
}

const relativeTimeFormatter = new Intl.RelativeTimeFormat(activeLocale(), { numeric: "always" });

function ApiKeysManager({ orgId, orgSlug, sites }: { orgId: number; orgSlug: string; sites: ApiKeySite[] }) {
  const queryClient = useQueryClient();
  const [secret, setSecret] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [scopes, setScopes] = useState<string[]>(["stats:read"]);
  const [siteAccess, setSiteAccess] = useState<"all" | "restricted">("all");
  const [siteIds, setSiteIds] = useState<number[]>([]);
  const [expiry, setExpiry] = useState("");
  const [validation, setValidation] = useState("");
  const [revokeKey, setRevokeKey] = useState<{ id: number; name: string } | null>(null);
  const key = ["api-keys", orgId];
  const keys = useQuery({
    queryKey: key,
    queryFn: () =>
      privateRequest<ListResponse<ApiKey>>(
        "GET",
        `/api/private/orgs/${orgId}/api-keys`,
        { orgId },
      ),
  });
  const create = useMutation({
    mutationFn: (input: CreateKeyInput) =>
      privateRequest<{ id: number; key: string }>(
        "POST",
        `/api/private/orgs/${orgId}/api-keys`,
        { orgId, body: input },
      ),
    onSuccess: (data) => {
      setSecret(data.key);
      setCreateOpen(false);
      setScopes(["stats:read"]);
      setSiteAccess("all");
      setSiteIds([]);
      setExpiry("");
      setValidation("");
      void queryClient.invalidateQueries({ queryKey: key });
    },
  });
  const revoke = useMutation({
    mutationFn: (id: number) =>
      privateRequest<void>(
        "DELETE",
        `/api/private/orgs/${orgId}/api-keys/${id}`,
        { orgId },
      ),
    onSuccess: () => {
      setRevokeKey(null);
      void queryClient.invalidateQueries({ queryKey: key });
    },
  });

  function resetCreateForm() {
    setScopes(["stats:read"]);
    setSiteAccess("all");
    setSiteIds([]);
    setExpiry("");
    setValidation("");
    create.reset();
  }

  function submitCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const name = String(form.get("name") ?? "").trim();
    if (scopes.length === 0) {
      setValidation(en.apiKeys.scopesRequired);
      return;
    }
    if (siteAccess === "restricted" && siteIds.length === 0) {
      setValidation(en.apiKeys.sitesRequired);
      return;
    }
    if (expiry) {
      const today = new Date().toISOString().slice(0, 10);
      if (expiry <= today) {
        setValidation(en.apiKeys.expiryFuture);
        return;
      }
    }
    setValidation("");
    create.mutate({
      name,
      scopes,
      site_ids: siteAccess === "all" ? null : siteIds,
      ...(expiry ? { expires_at: new Date(`${expiry}T00:00:00.000Z`).toISOString() } : {}),
    });
  }

  return (
    <Page
      title={en.apiKeys.title}
      description={en.apiKeys.description}
    >
      <Surface>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex flex-wrap gap-2">
            <Button asChild variant="link"><a href={`/app/${orgSlug}/settings/api`}>{en.apiKeys.apiReference}</a></Button>
            <Button asChild variant="link"><a href="/docs/api-auth">{en.apiKeys.authGuide}</a></Button>
          </div>
          <Button type="button" onClick={() => setCreateOpen(true)}>
            <KeyRoundIcon data-icon="inline-start" />
            {en.apiKeys.create}
          </Button>
        </div>
        <Message error={keys.error} />
        {keys.data?.data.length === 0 ? <p className="text-sm text-muted-foreground">{en.apiKeys.empty}</p> : null}
        {keys.data?.data.length ? (
          <Table aria-label={en.apiKeys.tableLabel}>
            <TableHeader>
              <TableRow>
                {Object.values(en.apiKeys.columns).map((column) => <TableHead key={column}>{column}</TableHead>)}
              </TableRow>
            </TableHeader>
            <TableBody>
              {keys.data.data.map((apiKey) => {
                const id = apiKeyValue<number>(apiKey, "id", "ID") ?? 0;
                const name = apiKeyValue<string>(apiKey, "name", "Name") ?? "";
                const prefix = apiKeyValue<string>(apiKey, "prefix", "Prefix") ?? "";
                const keyScopes = apiKeyValue<string[]>(apiKey, "scopes", "Scopes") ?? [];
                const keySiteIDs = apiKeyValue<number[] | null>(apiKey, "site_ids", "SiteIDs");
                const createdAt = apiKeyValue<string>(apiKey, "created_at", "CreatedAt");
                const expiresAt = apiKeyValue<string | null>(apiKey, "expires_at", "ExpiresAt");
                const lastUsedAt = apiKeyValue<string | null>(apiKey, "last_used_at", "LastUsedAt");
                const status = apiKeyStatus(apiKey);
                const siteNames = keySiteIDs == null
                  ? en.apiKeys.allSites
                  : keySiteIDs.map((siteID) => sites.find((site) => site.id === siteID)?.name ?? `#${siteID}`).join(", ");
                return (
                  <TableRow key={id}>
                    <TableCell>{name}</TableCell>
                    <TableCell><code>{prefix}</code></TableCell>
                    <TableCell><div className="flex flex-wrap gap-1">{keyScopes.map((scope) => <Badge variant="outline" key={scope}>{scope}</Badge>)}</div></TableCell>
                    <TableCell>{siteNames}</TableCell>
                    <TableCell>{createdAt ? fmt.date(createdAt, "UTC") : "—"}</TableCell>
                    <TableCell>{expiresAt ? fmt.date(expiresAt, "UTC") : en.apiKeys.never}</TableCell>
                    <TableCell>{lastUsedAt ? relativeTime(lastUsedAt) : en.apiKeys.never}</TableCell>
                    <TableCell><Badge variant={status === "active" ? "secondary" : "outline"}>{en.apiKeys[status]}</Badge></TableCell>
                    <TableCell>
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={status === "revoked"}
                        aria-label={`${en.apiKeys.revoke} ${name}`}
                        onClick={() => setRevokeKey({ id, name })}
                      >
                        {en.apiKeys.revoke}
                      </Button>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        ) : null}
      </Surface>

      <Dialog
        open={createOpen}
        onOpenChange={(open) => {
          setCreateOpen(open);
          if (!open) resetCreateForm();
        }}
      >
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{en.apiKeys.createTitle}</DialogTitle>
            <DialogDescription>{en.apiKeys.createDescription}</DialogDescription>
          </DialogHeader>
          <form className="flex flex-col gap-5" onSubmit={submitCreate}>
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="api-key-name">{en.apiKeys.name}</FieldLabel>
                <Input id="api-key-name" name="name" placeholder={en.apiKeys.namePlaceholder} required />
              </Field>
              <FieldSet>
                <FieldLegend>{en.apiKeys.scopes}</FieldLegend>
                <FieldGroup data-slot="checkbox-group" className="gap-3">
                  {apiKeyScopes.map((scope) => (
                    <Field orientation="horizontal" key={scope}>
                      <Checkbox
                        checked={scopes.includes(scope)}
                        id={`api-key-scope-${scope}`}
                        onCheckedChange={(checked) => setScopes((current) =>
                          checked === true
                            ? [...current, scope]
                            : current.filter((value) => value !== scope),
                        )}
                      />
                      <FieldContent>
                        <FieldLabel htmlFor={`api-key-scope-${scope}`}>{en.apiKeys.scopeLabels[scope]}</FieldLabel>
                        <FieldDescription>{en.apiKeys.scopeDescriptions[scope]}</FieldDescription>
                        {scope === "keys:manage" ? <FieldDescription>{en.apiKeys.keysManageWarning}</FieldDescription> : null}
                      </FieldContent>
                    </Field>
                  ))}
                </FieldGroup>
              </FieldSet>
              <Field>
                <FieldLabel htmlFor="api-key-site-access">{en.apiKeys.siteAccess}</FieldLabel>
                <Select value={siteAccess} onValueChange={(value) => setSiteAccess(value as "all" | "restricted")}>
                  <SelectTrigger id="api-key-site-access" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      <SelectItem value="all">{en.apiKeys.allSites}</SelectItem>
                      <SelectItem value="restricted">{en.apiKeys.onlySites}</SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </Field>
              {siteAccess === "restricted" ? (
                <FieldSet>
                  <FieldLegend variant="label">{en.apiKeys.onlySites}</FieldLegend>
                  <FieldGroup data-slot="checkbox-group" className="gap-3">
                    {sites.map((site) => (
                      <Field orientation="horizontal" key={site.id}>
                        <Checkbox
                          checked={siteIds.includes(site.id)}
                          id={`api-key-site-${site.id}`}
                          onCheckedChange={(checked) => setSiteIds((current) =>
                            checked === true
                              ? [...current, site.id]
                              : current.filter((id) => id !== site.id),
                          )}
                        />
                        <FieldLabel htmlFor={`api-key-site-${site.id}`}>{site.name}</FieldLabel>
                      </Field>
                    ))}
                  </FieldGroup>
                </FieldSet>
              ) : null}
              <Field data-invalid={validation === en.apiKeys.expiryFuture || undefined}>
                <FieldLabel htmlFor="api-key-expiry">{en.apiKeys.expiry}</FieldLabel>
                <Input id="api-key-expiry" type="date" value={expiry} aria-invalid={validation === en.apiKeys.expiryFuture || undefined} onChange={(event) => setExpiry(event.target.value)} />
                <FieldDescription>{en.apiKeys.expiryDescription}</FieldDescription>
              </Field>
            </FieldGroup>
            {validation ? <FieldError>{validation}</FieldError> : null}
            <Message error={create.error} />
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => { setCreateOpen(false); resetCreateForm(); }}>{en.common.close}</Button>
              <Button type="submit" disabled={create.isPending}>
                <KeyRoundIcon data-icon="inline-start" />
                {en.apiKeys.create}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <AlertDialog open={Boolean(revokeKey)} onOpenChange={(open) => open ? undefined : setRevokeKey(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{en.apiKeys.revokeTitle}</AlertDialogTitle>
            <AlertDialogDescription>{en.apiKeys.revokeDescription}</AlertDialogDescription>
          </AlertDialogHeader>
          <Message error={revoke.error} />
          <AlertDialogFooter>
            <AlertDialogCancel>{en.common.close}</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={revoke.isPending}
              onClick={(event) => {
                event.preventDefault();
                if (revokeKey) revoke.mutate(revokeKey.id);
              }}
            >
              {en.apiKeys.revokeConfirm}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      {secret ? (
        <ApiKeySecretDialog secret={secret} onClose={() => setSecret("")} />
      ) : null}
    </Page>
  );
}

export function AuditPage({ orgId }: { orgId: number }) {
  const [filter, setFilter] = useState("");
  const audit = useQuery({
    queryKey: ["audit", orgId],
    queryFn: () =>
      privateRequest<ListResponse<AuditEntry>>(
        "GET",
        `/api/private/orgs/${orgId}/audit?limit=100`,
        { orgId },
      ),
  });
  const entries =
    audit.data?.data.filter((entry) =>
      `${entry.action} ${entry.target_type}`
        .toLowerCase()
        .includes(filter.toLowerCase()),
    ) ?? [];
  return (
    <Page
      title="Audit log"
      description="Review security-sensitive changes made inside this organization."
    >
      <Surface>
        <Input
          aria-label="Filter audit log"
          placeholder="Filter actions"
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
        />
        <div className="overflow-x-auto" tabIndex={0}>
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b">
                <th className="py-2">When</th>
                <th>Action</th>
                <th>Target</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((entry) => (
                <tr className="border-b" key={entry.id}>
                  <td className="py-3 pr-4">
                    {fmt.date(entry.created_at, "UTC")}
                  </td>
                  <td className="pr-4">{entry.action}</td>
                  <td>
                    {entry.target_type} {entry.target_id}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Surface>
    </Page>
  );
}

export function AccountProfilePage({
  name,
  email,
}: {
  name: string;
  email: string;
}) {
  return (
    <Page
      title="Profile"
      description="Your identity across every organization."
    >
      <Surface>
        <FieldGroup>
          <Field>
            <FieldLabel>Name</FieldLabel>
            <Input value={name} readOnly />
          </Field>
          <Field>
            <FieldLabel>Email</FieldLabel>
            <Input value={email} readOnly />
          </Field>
        </FieldGroup>
        <FieldDescription>
          Profile updates are not exposed by this server version.
        </FieldDescription>
      </Surface>
    </Page>
  );
}

export function AccountSecurityPage() {
  const [enrollment, setEnrollment] = useState<{
    url: string;
    recovery_codes: string[];
  } | null>(null);
  const enroll = useMutation({
    mutationFn: () =>
      privateRequest<{ url: string; recovery_codes: string[] }>(
        "POST",
        "/api/private/me/totp",
      ),
    onSuccess: setEnrollment,
  });
  const confirm = useMutation({
    mutationFn: (code: string) =>
      privateRequest<void>("PATCH", "/api/private/me/totp", { body: { code } }),
    onSuccess: () => setEnrollment(null),
  });
  return (
    <Page
      title="Account security"
      description="Add a second factor and keep recovery codes offline."
    >
      <Surface>
        <div className="flex items-center gap-3">
          <ShieldCheckIcon />
          <div>
            <h2 className="font-medium">Authenticator app</h2>
            <p className="text-sm text-muted-foreground">
              Generate the QR code entirely in this browser.
            </p>
          </div>
        </div>
        {enrollment ? (
          <div className="flex flex-col gap-4">
            <TotpQr uri={enrollment.url} />
            <div>
              <p className="text-sm font-medium">Recovery codes — shown once</p>
              <ul className="grid grid-cols-2 gap-1 font-mono text-xs">
                {enrollment.recovery_codes.map((code) => (
                  <li key={code}>{code}</li>
                ))}
              </ul>
            </div>
            <form
              className="flex gap-2"
              onSubmit={(event) => {
                event.preventDefault();
                confirm.mutate(
                  String(new FormData(event.currentTarget).get("code") ?? ""),
                );
              }}
            >
              <Input
                aria-label="Authentication code"
                name="code"
                inputMode="numeric"
                required
              />
              <Button type="submit">Confirm</Button>
            </form>
          </div>
        ) : (
          <Button className="self-start" onClick={() => enroll.mutate()}>
            Set up authenticator
          </Button>
        )}
      </Surface>
      <Surface>
        <h2 className="font-medium">Password</h2>
        <p className="text-sm text-muted-foreground">
          Password changes are not exposed by this server version.
        </p>
      </Surface>
    </Page>
  );
}

export { AccountSessionsPage } from "./AccountSessionsPage";
