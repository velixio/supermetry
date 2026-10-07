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

export function ApiKeySecretDialog({
  secret: initialSecret,
  onClose,
}: {
  secret: string;
  onClose: () => void;
}) {
  const [secret, setSecret] = useState(initialSecret);

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
          <DialogTitle>Copy your API key</DialogTitle>
          <DialogDescription>
            You won&apos;t see this key again. Store it somewhere safe before
            closing.
          </DialogDescription>
        </DialogHeader>
        <code className="break-all rounded-lg bg-muted p-3 text-xs">
          {secret}
        </code>
        <DialogFooter>
          <Button
            type="button"
            onClick={() => void copyText(secret)}
          >
            Copy API key
          </Button>
          <Button type="button" variant="outline" onClick={close}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
