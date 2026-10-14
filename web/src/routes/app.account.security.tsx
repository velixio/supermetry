import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { type FormEvent, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { ShieldCheckIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { TotpQr } from "@/features/settings/TotpQr";
import { privateRequest } from "@/features/settings/privateApi";
import { useMe } from "./app.$org";

type Method = "POST" | "PATCH" | "DELETE";
type Enrollment = { url: string; recovery_codes: string[] };
type Request = (method: Method, path: string, body: Record<string, string>) => Promise<unknown>;
type ReauthAction = "setup" | "disable" | "regenerate";

export const Route = createFileRoute("/app/account/security")({ component: AccountSecurityRoute });

function AccountSecurityRoute() {
  const me = useMe();
  return me.data ? <AccountSecurityPage totpEnabled={Boolean(me.data.user.totp_enabled)} /> : null;
}

export function AccountSecurityPage({ totpEnabled: initialEnabled, request = (method, path, body) => privateRequest(method, path, { body }) }: { totpEnabled: boolean; request?: Request }) {
  const queryClient = useQueryClient();
  const [enabled, setEnabled] = useState(initialEnabled);
  const [action, setAction] = useState<ReauthAction | null>(null);
  const [enrollment, setEnrollment] = useState<Enrollment | null>(null);
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);
  const mutation = useMutation({
    mutationFn: ({ action: next, body }: { action: ReauthAction; body: Record<string, string> }) => next === "setup"
      ? request("POST", "/api/private/me/totp", body)
      : next === "disable"
        ? request("DELETE", "/api/private/me/totp", body)
        : request("POST", "/api/private/me/totp/recovery-codes", body),
    onSuccess: (result, variables) => {
      if (variables.action === "setup") {
        setEnrollment(result as Enrollment);
        setAction(null);
      } else if (variables.action === "disable") {
        setEnabled(false);
        setAction(null);
        void queryClient.invalidateQueries({ queryKey: ["me"] });
      } else {
        setRecoveryCodes((result as { recovery_codes: string[] }).recovery_codes);
      }
    },
  });
  const confirm = useMutation({
    mutationFn: (code: string) => request("PATCH", "/api/private/me/totp", { code }),
    onSuccess: () => {
      setEnrollment(null);
      setEnabled(true);
      void queryClient.invalidateQueries({ queryKey: ["me"] });
    },
  });
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!action) return;
    const data = new FormData(event.currentTarget);
    mutation.mutate({ action, body: { current_password: String(data.get("current_password") ?? ""), code: String(data.get("code") ?? "") } });
  };
  const title = action === "disable" ? "Disable 2FA" : action === "regenerate" ? "Regenerate recovery codes" : "Set up authenticator";
  return <PageContainer width="narrow">
    <PageHeader title="Account security" description="Protect your account with an authenticator and offline recovery codes." />
    <Card>
      <CardHeader><CardTitle className="flex items-center gap-2"><ShieldCheckIcon data-icon="inline-start" />Authenticator app</CardTitle><CardDescription>{enabled ? "Two-factor authentication is enabled." : "Require a time-based code when signing in."}</CardDescription></CardHeader>
      <CardContent>{enrollment ? <div className="flex flex-col gap-4"><TotpQr uri={enrollment.url} /><RecoveryCodeList codes={enrollment.recovery_codes} /><form className="flex gap-2" onSubmit={(event) => { event.preventDefault(); confirm.mutate(String(new FormData(event.currentTarget).get("code") ?? "")); }}><Input aria-label="New authenticator code" name="code" inputMode="numeric" required /><Button type="submit" disabled={confirm.isPending}>Confirm</Button></form>{confirm.error ? <FieldError>{confirm.error.message}</FieldError> : null}</div> : null}</CardContent>
      {!enrollment ? <CardFooter className="flex flex-wrap gap-2">{enabled ? <><Button variant="outline" onClick={() => { setRecoveryCodes(null); setAction("regenerate"); }}>Regenerate recovery codes</Button><Button variant="destructive" onClick={() => { setRecoveryCodes(null); setAction("disable"); }}>Disable authenticator</Button></> : <Button onClick={() => { setRecoveryCodes(null); setAction("setup"); }}>Set up authenticator</Button>}</CardFooter> : null}
    </Card>
    <Dialog open={action !== null} onOpenChange={(open) => { if (!open) { setAction(null); setRecoveryCodes(null); mutation.reset(); } }}><DialogContent><DialogHeader><DialogTitle>{title}</DialogTitle><DialogDescription>Enter your current password{enabled ? " and a current authenticator or recovery code" : ""}.</DialogDescription></DialogHeader>{recoveryCodes ? <><RecoveryCodeList codes={recoveryCodes} /><DialogFooter><Button onClick={() => { setAction(null); setRecoveryCodes(null); }}>Done</Button></DialogFooter></> : <form className="flex flex-col gap-4" onSubmit={submit}><FieldGroup><Field><FieldLabel htmlFor="totp-current-password">Current password</FieldLabel><Input id="totp-current-password" name="current_password" type="password" required /></Field>{enabled ? <Field><FieldLabel htmlFor="totp-current-code">Authentication or recovery code</FieldLabel><Input id="totp-current-code" name="code" autoComplete="one-time-code" required /><FieldDescription>Each authenticator code can be used only once.</FieldDescription></Field> : null}{mutation.error ? <FieldError>{mutation.error.message}</FieldError> : null}</FieldGroup><DialogFooter><Button type="button" variant="outline" onClick={() => setAction(null)}>Cancel</Button><Button type="submit" variant={action === "disable" ? "destructive" : "default"} disabled={mutation.isPending}>{action === "disable" ? "Disable 2FA" : action === "regenerate" ? "Regenerate codes" : "Continue"}</Button></DialogFooter></form>}</DialogContent></Dialog>
  </PageContainer>;
}

function RecoveryCodeList({ codes }: { codes: string[] }) {
  return <div className="flex flex-col gap-2"><p className="text-sm font-medium">Recovery codes — shown once</p><ul className="grid grid-cols-2 gap-1 font-mono text-xs">{codes.map((code) => <li key={code}>{code}</li>)}</ul></div>;
}
