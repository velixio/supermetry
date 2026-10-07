import { useMemo, useState } from "react";
import { CheckIcon, CopyIcon } from "lucide-react";

import { Button } from "@/components/ui/button";

export function trackingSnippet(publicId: string): string {
  return `<script defer data-site="${publicId}" src="/p.js"></script>`;
}

export function TrackingSnippet({ publicId }: { publicId: string }) {
  const [copied, setCopied] = useState(false);
  const snippet = useMemo(() => trackingSnippet(publicId), [publicId]);

  async function copy() {
    await navigator.clipboard.writeText(snippet);
    setCopied(true);
  }

  return (
    <div className="flex flex-col gap-3">
      <pre className="overflow-x-auto rounded-lg bg-muted p-3 text-xs">
        <code>{snippet}</code>
      </pre>
      <div className="flex items-center gap-3">
        <Button type="button" variant="outline" onClick={() => void copy()}>
          {copied ? (
            <CheckIcon data-icon="inline-start" />
          ) : (
            <CopyIcon data-icon="inline-start" />
          )}
          Copy tracking snippet
        </Button>
        {copied ? (
          <span role="status" className="text-sm text-muted-foreground">
            Copied
          </span>
        ) : null}
      </div>
    </div>
  );
}
