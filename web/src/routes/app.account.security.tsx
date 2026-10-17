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
import { Checkbox } from "@/components/ui/checkbox";
import { PasswordInput } from "@/features/auth/PasswordInput";
import { copyText } from "@/lib/clipboard";
import { uiFlows } from "@/messages/uiFlows";
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
  const [savedCodes, setSavedCodes] = useState(false);
  const [notice, setNotice] = useState("");
  const mutation = useMutation({
    mutationFn: ({ action: next, body }: { action: ReauthAction; body: Record<string, string> }) => next === "setup"
      ? request("POST", "/api/private/me/totp", body)
      : next === "disable"
        ? request("DELETE", "/api/private/me/totp", body)
        : request("POST", "/api/private/me/totp/recovery-codes", body),
    onSuccess: (result, variables) => {
      if (variables.action === "setup") {
        setEnrollment(result as Enrollment);
        setSavedCodes(false);
        setAction(null);
      } else if (variables.action === "disable") {
        setEnabled(false);
        setAction(null);
        setNotice(uiFlows.totp.disabled);
        void queryClient.invalidateQueries({ queryKey: ["me"] });
      } else {
        setRecoveryCodes((result as { recovery_codes: string[] }).recovery_codes);
        setNotice(uiFlows.totp.regenerated);
      }
    },
  });
  const confirm = useMutation({
    mutationFn: (code: string) => request("PATCH", "/api/private/me/totp", { code }),
    onSuccess: () => {
      setEnrollment(null);
      setEnabled(true);
      setNotice(uiFlows.totp.enabled);
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
      <CardContent className="flex flex-col gap-4">
        {notice ? <p role="status" className="text-sm text-muted-foreground">{notice}</p> : null}
        {enrollment ? <div className="flex flex-col gap-4">
          <TotpQr uri={enrollment.url} />
          <ManualSetupKey uri={enrollment.url} />
          <RecoveryCodeList codes={enrollment.recovery_codes} />
          <Field orientation="horizontal">
            <Checkbox id="totp-saved-codes" checked={savedCodes} onCheckedChange={(checked) => setSavedCodes(checked === true)} />
            <FieldLabel htmlFor="totp-saved-codes" className="font-normal">{uiFlows.totp.savedCodes}</FieldLabel>
          </Field>
          <form className="flex flex-col gap-3" onSubmit={(event) => { event.preventDefault(); confirm.mutate(String(new FormData(event.currentTarget).get("code") ?? "")); }}>
            <Field data-invalid={Boolean(confirm.error) || undefined}>
              <FieldLabel htmlFor="totp-confirm-code">{uiFlows.totp.confirmCode}</FieldLabel>
              <Input id="totp-confirm-code" name="code" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" maxLength={6} required aria-invalid={Boolean(confirm.error) || undefined} aria-describedby={confirm.error ? "totp-confirm-help totp-confirm-error" : "totp-confirm-help"} />
              <FieldDescription id="totp-confirm-help">{uiFlows.totp.confirmHelp}</FieldDescription>
              {confirm.error ? <FieldError id="totp-confirm-error">{confirm.error.message}</FieldError> : null}
            </Field>
            <Button type="submit" className="self-start" disabled={confirm.isPending || !savedCodes}>{confirm.isPending ? uiFlows.totp.confirming : "Confirm"}</Button>
          </form>
        </div> : null}
      </CardContent>
      {!enrollment ? <CardFooter className="flex flex-wrap gap-2">{enabled ? <><Button variant="outline" onClick={() => { setNotice(""); setRecoveryCodes(null); setAction("regenerate"); }}>Regenerate recovery codes</Button><Button variant="destructive" onClick={() => { setNotice(""); setRecoveryCodes(null); setAction("disable"); }}>Disable authenticator</Button></> : <Button onClick={() => { setNotice(""); setRecoveryCodes(null); setAction("setup"); }}>Set up authenticator</Button>}</CardFooter> : null}
    </Card>
    <Dialog open={action !== null} onOpenChange={(open) => { if (!open) { setAction(null); setRecoveryCodes(null); mutation.reset(); } }}><DialogContent><DialogHeader><DialogTitle>{title}</DialogTitle><DialogDescription>Enter your current password{enabled ? " and a current authenticator or recovery code" : ""}.</DialogDescription></DialogHeader>{recoveryCodes ? <><RecoveryCodeList codes={recoveryCodes} /><DialogFooter><Button onClick={() => { setAction(null); setRecoveryCodes(null); }}>Done</Button></DialogFooter></> : <form className="flex flex-col gap-4" onSubmit={submit}><FieldGroup><Field><FieldLabel htmlFor="totp-current-password">Current password</FieldLabel><PasswordInput id="totp-current-password" name="current_password" autoComplete="current-password" required /></Field>{enabled ? <Field><FieldLabel htmlFor="totp-current-code">Authentication or recovery code</FieldLabel><Input id="totp-current-code" name="code" autoComplete="one-time-code" spellCheck={false} autoCapitalize="none" required /><FieldDescription>Each authenticator code can be used only once.</FieldDescription></Field> : null}{mutation.error ? <FieldError>{mutation.error.message}</FieldError> : null}</FieldGroup><DialogFooter><Button type="button" variant="outline" onClick={() => setAction(null)}>Cancel</Button><Button type="submit" variant={action === "disable" ? "destructive" : "default"} disabled={mutation.isPending}>{mutation.isPending ? uiFlows.common.saving : action === "disable" ? "Disable 2FA" : action === "regenerate" ? "Regenerate codes" : "Continue"}</Button></DialogFooter></form>}</DialogContent></Dialog>
  </PageContainer>;
}

/** The base32 secret of an otpauth:// URI, grouped in fours for typing. */
export function totpSecret(uri: string): string {
  try {
    const secret = new URL(uri).searchParams.get("secret") ?? "";
    return secret.replace(/\s+/g, "").toUpperCase().replace(/(.{4})(?=.)/g, "$1 ");
  } catch {
    return "";
  }
}

function CopyStatus({ state }: { state: "yes" | "manual" | null }) {
  return state ? <span role="status" className="text-sm text-muted-foreground">{state === "yes" ? uiFlows.common.copied : uiFlows.common.copyManual}</span> : null;
}

function useCopy() {
  const [state, setState] = useState<"yes" | "manual" | null>(null);
  const copy = (text: string) => {
    setState(null);
    void copyText(text).then((ok) => setState(ok ? "yes" : "manual"));
  };
  return { state, copy };
}

function ManualSetupKey({ uri }: { uri: string }) {
  const secret = totpSecret(uri);
  const { state, copy } = useCopy();
  if (!secret) return null;
  return <div className="flex flex-col gap-2">
    <p className="text-sm text-muted-foreground" id="totp-manual-key-label">{uiFlows.totp.manualKey}</p>
    <code aria-labelledby="totp-manual-key-label" className="w-fit rounded-md bg-muted px-3 py-2 font-mono text-sm tracking-wider select-all break-all">{secret}</code>
    <div className="flex flex-wrap items-center gap-3"><Button type="button" variant="outline" size="sm" onClick={() => copy(secret.replace(/ /g, ""))}>{uiFlows.totp.copyKey}</Button><CopyStatus state={state} /></div>
  </div>;
}

function RecoveryCodeList({ codes }: { codes: string[] }) {
  const { state, copy } = useCopy();
  const text = `${codes.join("\n")}\n`;
  const download = () => {
    const url = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = "supermetry-recovery-codes.txt";
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(url);
  };
  return <section className="flex flex-col gap-2" aria-labelledby="recovery-codes-title">
    <h3 id="recovery-codes-title" className="text-sm font-medium">{uiFlows.totp.recoveryTitle}</h3>
    <p className="text-sm text-muted-foreground">{uiFlows.totp.recoveryNote}</p>
    <ul className="grid grid-cols-2 gap-1 rounded-md bg-muted p-3 font-mono text-xs">{codes.map((code) => <li key={code}>{code}</li>)}</ul>
    <div className="flex flex-wrap items-center gap-2">
      <Button type="button" variant="outline" size="sm" onClick={() => copy(text)}>{uiFlows.totp.copyCodes}</Button>
      <Button type="button" variant="outline" size="sm" onClick={download}>{uiFlows.totp.downloadCodes}</Button>
      <CopyStatus state={state} />
    </div>
  </section>;
}
