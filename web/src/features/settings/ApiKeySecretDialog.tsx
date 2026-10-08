import { useState } from "react";
import { copyText } from "@/lib/clipboard";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field";
import { en } from "@/messages/en";

export function ApiKeySecretDialog({
  secret: initialSecret,
  onClose,
}: {
  secret: string;
  onClose: () => void;
}) {
  const [secret, setSecret] = useState(initialSecret);
  const curl = `curl ${window.location.origin}/api/public/v1/sites \\\n+  -H "Accept: application/json" \\\n+  -H "Authorization: Bearer ${secret}"`;

  function close() {
    setSecret("");
    onClose();
  }

  return (
    <Dialog
      open={secret.length > 0}
      onOpenChange={(open) => (open ? undefined : close())}
    >
      <DialogContent showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>{en.apiKeys.secretTitle}</DialogTitle>
          <DialogDescription>{en.apiKeys.secretDescription}</DialogDescription>
        </DialogHeader>
        <Field>
          <FieldLabel htmlFor="created-api-key">{en.apiKeys.secretLabel}</FieldLabel>
          <Input id="created-api-key" readOnly value={secret} />
        </Field>
        <Field>
          <FieldLabel>{en.apiKeys.curlLabel}</FieldLabel>
          <FieldDescription>{en.apiKeys.curlDescription}</FieldDescription>
          <pre className="overflow-x-auto rounded-lg bg-muted p-3 text-xs"><code>{curl}</code></pre>
        </Field>
        <DialogFooter>
          <Button
            type="button"
            onClick={() => void copyText(secret)}
          >
            {en.apiKeys.copyKey}
          </Button>
          <Button type="button" variant="outline" onClick={() => void copyText(curl)}>
            {en.apiKeys.copyCurl}
          </Button>
          <Button type="button" variant="outline" onClick={close}>
            {en.common.close}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
