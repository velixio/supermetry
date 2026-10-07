import { useMemo, useState } from "react";
import { CheckIcon, CopyIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { copyText } from "@/lib/clipboard";

// Fallback used only until the server snippet loads: absolute URL on this origin.
export function trackingSnippet(publicId: string, origin = window.location.origin): string {
  return `<script defer data-site="${publicId}" src="${origin}/js/script.js"></script>`;
}

export function TrackingSnippet({ publicId, snippet: serverSnippet }: { publicId: string; snippet?: string }) {
  const [copied, setCopied] = useState<"yes" | "manual" | null>(null);
  const snippet = useMemo(() => serverSnippet ?? trackingSnippet(publicId), [serverSnippet, publicId]);

  async function copy() {
    setCopied((await copyText(snippet)) ? "yes" : "manual");
  }

  return (
    <div className="flex flex-col gap-3">
      <pre className="overflow-x-auto rounded-lg bg-muted p-3 text-xs select-all">
        <code>{snippet}</code>
      </pre>
      <div className="flex items-center gap-3">
        <Button type="button" variant="outline" onClick={() => void copy()}>
          {copied === "yes" ? (
            <CheckIcon data-icon="inline-start" />
          ) : (
            <CopyIcon data-icon="inline-start" />
          )}
          Copy tracking snippet
        </Button>
        {copied ? (
          <span role="status" className="text-sm text-muted-foreground">
            {copied === "yes" ? "Copied" : "Select the snippet and press Ctrl+C to copy"}
          </span>
        ) : null}
      </div>
    </div>
  );
}
