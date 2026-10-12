import { useMemo, useState } from "react";
import { CheckIcon, CopyIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { copyText } from "@/lib/clipboard";

// Fallback used only until the server snippet loads: absolute URL on this origin.
export function trackingSnippet(
  publicId: string,
  origin = window.location.origin,
  full = true,
): string {
  return `<script defer data-site="${publicId}" src="${origin}/js/script${full ? ".full" : ""}.js"></script>`;
}

const scriptOptions = [
  {
    id: "full",
    full: true,
    title: "Full script (recommended)",
    size: "~2.3 KB",
    collects:
      "Everything in core, plus outbound links, file downloads, 404 pages, data-supermetry-event clicks, Web Vitals and heatmap clicks.",
  },
  {
    id: "core",
    full: false,
    title: "Core script",
    size: "under 1 KB",
    collects:
      "Pageviews (including single-page apps), scroll depth, engagement time, supermetry.track() events, revenue and supermetry.identify().",
  },
] as const;

export function TrackingSnippet({
  publicId,
  snippet: serverSnippet,
  fullSnippet,
}: {
  publicId: string;
  snippet?: string;
  fullSnippet?: string;
}) {
  const [copied, setCopied] = useState<"yes" | "manual" | null>(null);
  const [full, setFull] = useState(true);
  const generatedSnippet = Boolean(
    serverSnippet && fullSnippet && serverSnippet === fullSnippet,
  );
  const snippet = useMemo(
    () =>
      (generatedSnippet ? serverSnippet : full ? fullSnippet : serverSnippet) ??
      trackingSnippet(publicId, window.location.origin, full),
    [full, fullSnippet, generatedSnippet, publicId, serverSnippet],
  );

  async function copy() {
    setCopied((await copyText(snippet)) ? "yes" : "manual");
  }

  return (
    <div className="flex flex-col gap-3">
      {generatedSnippet ? (
        <div className="flex flex-col gap-1 text-sm text-muted-foreground">
          <p>This URL applies the tracker features selected in Site settings → Tracking, and session replay when it is on.</p>
          <p className="text-xs">
            Installed an older snippet with <code>/js/script.js</code> or <code>/js/script.full.js</code>? Replace it with
            this one: session replay only starts from this URL.
          </p>
        </div>
      ) : (
        <>
          <fieldset className="grid gap-3 sm:grid-cols-2">
            <legend className="mb-2 text-sm font-medium">Choose a script</legend>
            {scriptOptions.map((option) => (
              <label
                key={option.id}
                className="flex cursor-pointer flex-col gap-2 rounded-lg border p-3 text-sm has-[:checked]:border-accent has-[:checked]:bg-accent/5"
              >
                <span className="flex items-center gap-2 font-medium">
                  <input
                    type="radio"
                    name="tracker-script"
                    value={option.id}
                    checked={full === option.full}
                    onChange={() => setFull(option.full)}
                    className="size-4 accent-accent"
                  />
                  {option.title}
                  <span className="ml-auto text-xs font-normal text-muted-foreground">{option.size}</span>
                </span>
                <span className="text-muted-foreground">{option.collects}</span>
              </label>
            ))}
          </fieldset>
          <p className="text-xs text-muted-foreground">
            Already installed the core script? Change <code>/js/script.js</code> to <code>/js/script.full.js</code> in your snippet to switch.
          </p>
        </>
      )}
      <pre className="overflow-x-auto rounded-lg bg-muted p-3 text-xs select-all" tabIndex={0}>
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
