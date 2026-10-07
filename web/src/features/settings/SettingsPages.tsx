import { useState, type FormEvent, type ReactNode } from "react";
import { copyText } from "@/lib/clipboard";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ExternalLinkIcon, KeyRoundIcon, ShieldCheckIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { fmt } from "@/lib/format";

import { ApiKeySecretDialog } from "./ApiKeySecretDialog";
import { MemberRoleSelect, type MemberRole } from "./MemberRoleSelect";
import { privateRequest, type ListResponse } from "./privateApi";
import { TotpQr } from "./TotpQr";
import { InstallGuide, installationStatusInterval } from "./SiteSettings";

type Site = {
  id: number;
  public_id: string;
  name: string;
  allowed_hosts: string[];
  timezone: string;
  retention_days: number;
  last_event_received_at?: string;
};

type Member = {
  user_id: number;
  email: string;
  name: string;
  role: MemberRole;
  site_ids?: number[];
};
type Invitation = {
  id: number;
  email: string;
  role: MemberRole;
  expires_at: string;
  revoked_at?: string;
};
type ApiKey = {
  id?: number;
  ID?: number;
  name?: string;
  Name?: string;
  prefix?: string;
  Prefix?: string;
  revoked_at?: string;
  RevokedAt?: string;
};
type Share = { id: number; password_protected: boolean; revoked_at?: string };
type Report = { id: number; cadence: string; last_sent_at?: string };
type AuditEntry = {
  id: number;
  action: string;
  target_type: string;
  target_id: number;
  created_at: string;
};
type Session = {
  id?: string;
  ID?: string;
  device_label?: string;
  DeviceLabel?: string;
  last_seen_at?: string;
  LastSeenAt?: string;
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
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        <p className="text-sm text-muted-foreground">{description}</p>
      </header>
      {children}
    </div>
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
}: {
  siteId: number;
  orgId: number;
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
    save.mutate({
      name: String(data.get("name") ?? ""),
      hosts: String(data.get("hosts") ?? "")
        .split(",")
        .map((host) => host.trim())
        .filter(Boolean),
      timezone: String(data.get("timezone") ?? ""),
      retention_days: Number(data.get("retention")),
    });
  }
  return (
    <Page
      title="Site settings"
      description="Control identity, accepted hosts, local time, and raw-data retention."
    >
      <Surface>
        {site.data ? (
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
        ) : (
          <p className="text-sm text-muted-foreground">
            Loading site settings…
          </p>
        )}
      </Surface>
    </Page>
  );
}

export function InstallationPage({
  siteId,
  publicId,
  orgId,
}: {
  siteId: number;
  publicId: string;
  orgId: number;
}) {
  const status = useQuery({
    queryKey: ["tracking-status", siteId],
    queryFn: () =>
      privateRequest<Site>("GET", `/api/private/sites/${siteId}`, { orgId }),
    refetchInterval: (query) =>
      installationStatusInterval(query.state.data?.last_event_received_at),
  });
  const snippet = useQuery({
    queryKey: ["site-snippet", orgId, siteId],
    queryFn: () =>
      privateRequest<{ snippet: string }>("GET", `/api/private/sites/${siteId}/snippet`, { orgId }),
  });
  return (
    <Page
      title="Installation"
      description="Install the tracker once, then verify the first event arrives."
    >
      <Surface>
        <InstallGuide
          publicId={publicId}
          snippet={snippet.data?.snippet}
          lastEventReceivedAt={status.data?.last_event_received_at}
        />
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
            to={kind === "goals" ? "/$org/$site/goals" : "/$org/$site/funnels"}
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
  const create = useMutation({
    mutationFn: (password: string) =>
      privateRequest<{ slug: string }>(
        "POST",
        `/api/private/sites/${siteId}/shares`,
        { orgId, body: { password } },
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
  return (
    <Page
      title="Shared dashboards"
      description="Create revocable read-only links, with an optional password."
    >
      <Surface>
        <form
          className="flex flex-wrap gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            create.mutate(
              String(new FormData(event.currentTarget).get("password") ?? ""),
            );
          }}
        >
          <Input
            className="max-w-sm"
            aria-label="Optional share password"
            name="password"
            placeholder="Optional password"
          />
          <Button type="submit">Create link</Button>
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
          </div>
        ) : null}
        <ul className="flex flex-col gap-2">
          {shares.data?.data.map((share) => (
            <li
              className="flex items-center justify-between gap-3 border-t pt-3"
              key={share.id}
            >
              <span className="text-sm">
                Link {share.id}
                {share.password_protected ? " · Password protected" : ""}
              </span>
              <Button
                size="sm"
                variant="outline"
                disabled={Boolean(share.revoked_at)}
                onClick={() => revoke.mutate(share.id)}
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
      role,
      siteIds,
    }: {
      member: number;
      role: MemberRole;
      siteIds: number[];
    }) =>
      privateRequest<void>(
        "PATCH",
        `/api/private/orgs/${orgId}/members/${member}`,
        { orgId, body: { role, site_ids: siteIds } },
      ),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: key }),
  });
  return (
    <Page
      title="Members"
      description="Assign organization roles and restrict editors or viewers to specific sites."
    >
      <Surface>
        <ul className="flex flex-col gap-4">
          {members.data?.data.map((member) => {
            const locked = member.role === "owner" && actorRole !== "owner";
            return (
              <li
                className="grid gap-3 border-b pb-4 md:grid-cols-[minmax(0,1fr)_auto]"
                key={member.user_id}
              >
                <div>
                  <p className="text-sm font-medium">{member.name}</p>
                  <p className="text-xs text-muted-foreground">
                    {member.email}
                  </p>
                  <div className="mt-2 flex flex-wrap gap-3">
                    {sites.map((site) => (
                      <label
                        className="flex items-center gap-2 text-xs"
                        key={site.id}
                      >
                        <input
                          type="checkbox"
                          disabled={
                            locked ||
                            member.role === "owner" ||
                            member.role === "admin"
                          }
                          defaultChecked={
                            !member.site_ids ||
                            member.site_ids.includes(site.id)
                          }
                          onChange={(event) => {
                            const current = new Set(
                              member.site_ids ?? sites.map((item) => item.id),
                            );
                            event.target.checked
                              ? current.add(site.id)
                              : current.delete(site.id);
                            update.mutate({
                              member: member.user_id,
                              role: member.role,
                              siteIds: [...current],
                            });
                          }}
                        />
                        {site.name}
                      </label>
                    ))}
                  </div>
                </div>
                <MemberRoleSelect
                  actorRole={actorRole}
                  value={member.role}
                  disabled={locked}
                  onChange={(role) =>
                    update.mutate({
                      member: member.user_id,
                      role,
                      siteIds: member.site_ids ?? [],
                    })
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

export function InvitationsPage({ orgId }: { orgId: number }) {
  const queryClient = useQueryClient();
  const [link, setLink] = useState("");
  const key = ["invitations", orgId];
  const invitations = useQuery({
    queryKey: key,
    queryFn: () =>
      privateRequest<ListResponse<Invitation>>(
        "GET",
        `/api/private/orgs/${orgId}/invitations`,
        { orgId },
      ),
  });
  const create = useMutation({
    mutationFn: (email: string) =>
      privateRequest<{ link: string }>(
        "POST",
        `/api/private/orgs/${orgId}/invitations`,
        { orgId, body: { email, role: "viewer" } },
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
  return (
    <Page
      title="Invitations"
      description="Invite a teammate by email or share a copyable invitation link."
    >
      <Surface>
        <form
          className="flex gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            create.mutate(
              String(new FormData(event.currentTarget).get("email") ?? ""),
            );
          }}
        >
          <Input
            name="email"
            type="email"
            placeholder="teammate@example.com"
            required
          />
          <Button type="submit">Invite</Button>
        </form>
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
              className="flex items-center justify-between border-t pt-3"
              key={invitation.id}
            >
              <span className="text-sm">
                {invitation.email} · {invitation.role}
              </span>
              <Button
                size="sm"
                variant="outline"
                disabled={Boolean(invitation.revoked_at)}
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

export function ApiKeysPage({ orgId }: { orgId: number }) {
  const queryClient = useQueryClient();
  const [secret, setSecret] = useState("");
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
    mutationFn: (name: string) =>
      privateRequest<{ id: number; key: string }>(
        "POST",
        `/api/private/orgs/${orgId}/api-keys`,
        { orgId, body: { name, scopes: ["stats:read"] } },
      ),
    onSuccess: (data) => {
      setSecret(data.key);
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
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: key }),
  });
  return (
    <Page
      title="API keys"
      description="Create scoped integration keys. A new secret is displayed exactly once."
    >
      <Surface>
        <form
          className="flex gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            create.mutate(
              String(new FormData(event.currentTarget).get("name") ?? ""),
            );
          }}
        >
          <Input name="name" placeholder="Reporting integration" required />
          <Button type="submit">
            <KeyRoundIcon data-icon="inline-start" />
            Create key
          </Button>
        </form>
        <ul className="flex flex-col gap-2">
          {keys.data?.data.map((key) => {
            const id = key.id ?? key.ID ?? 0;
            return (
              <li
                className="flex items-center justify-between border-t pt-3"
                key={id}
              >
                <span className="text-sm">
                  {key.name ?? key.Name} · {key.prefix ?? key.Prefix}
                </span>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={Boolean(key.revoked_at ?? key.RevokedAt)}
                  onClick={() => revoke.mutate(id)}
                >
                  Revoke
                </Button>
              </li>
            );
          })}
        </ul>
      </Surface>
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
        <div className="overflow-x-auto">
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

export function AccountSessionsPage() {
  const queryClient = useQueryClient();
  const key = ["sessions"];
  const sessions = useQuery({
    queryKey: key,
    queryFn: () =>
      privateRequest<ListResponse<Session>>("GET", "/api/private/me/sessions"),
  });
  const revoke = useMutation({
    mutationFn: (id: string) =>
      privateRequest<void>(
        "DELETE",
        `/api/private/me/sessions/${encodeURIComponent(id)}`,
      ),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: key }),
  });
  return (
    <Page
      title="Active sessions"
      description="Review devices signed into your account and revoke any you do not recognize."
    >
      <Surface>
        <ul className="flex flex-col gap-3">
          {sessions.data?.data.map((session) => {
            const id = session.id ?? session.ID ?? "";
            return (
              <li
                className="flex items-center justify-between gap-3 border-b pb-3"
                key={id}
              >
                <div>
                  <p className="text-sm font-medium">
                    {session.device_label ??
                      session.DeviceLabel ??
                      "Unknown device"}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    Last active{" "}
                    {fmt.date(
                      session.last_seen_at ??
                        session.LastSeenAt ??
                        new Date().toISOString(),
                      "UTC",
                    )}
                  </p>
                </div>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => revoke.mutate(id)}
                >
                  Revoke
                </Button>
              </li>
            );
          })}
        </ul>
      </Surface>
    </Page>
  );
}
