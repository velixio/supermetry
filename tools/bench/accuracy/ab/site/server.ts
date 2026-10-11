import { readFileSync } from "node:fs";
import { join } from "node:path";

export type SiteOptions = {
  port?: number;
  plausibleURL?: string;
  plausibleVariant?: string;
  plausibleDomain?: string;
  supermetryScriptURL?: string;
};

export type SiteServer = {
  url: string;
  stop: () => Promise<void>;
};

const root = import.meta.dir;

function htmlAttribute(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll('"', "&quot;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

function trackerMarkup(options: Required<Omit<SiteOptions, "port">>): string {
  const plausibleSrc = `${options.plausibleURL.replace(/\/$/, "")}/js/script.${options.plausibleVariant}.js`;
  return `<script defer data-domain="${htmlAttribute(options.plausibleDomain)}" src="${htmlAttribute(plausibleSrc)}"></script>
<script defer src="${htmlAttribute(options.supermetryScriptURL)}"></script>
<script>
window.__track = function(name, props, revenue) {
  if (window.plausible) window.plausible(name, {props: props || {}, revenue: revenue});
  if (window.supermetry && window.supermetry.track) window.supermetry.track(name, props || {}, {revenue: revenue});
};
</script>`;
}

function injectTrackers(html: string, options: Required<Omit<SiteOptions, "port">>): string {
  return html.replace("</head>", `${trackerMarkup(options)}\n</head>`);
}

const routes = new Map<string, string>([
  ["/", "index.html"],
  ["/about", "about.html"],
  ["/pricing", "pricing.html"],
  ["/blog/post-1", "blog/post-1.html"],
  ["/spa", "spa.html"],
]);

export function startSite(options: SiteOptions = {}): SiteServer {
  const config = {
    plausibleURL: options.plausibleURL ?? process.env.PLAUSIBLE_URL ?? "http://127.0.0.1:8000",
    plausibleVariant: options.plausibleVariant ?? process.env.PLAUSIBLE_VARIANT ?? "file-downloads.outbound-links.revenue",
    plausibleDomain: options.plausibleDomain ?? process.env.PLAUSIBLE_DOMAIN ?? "site1.test",
    supermetryScriptURL: options.supermetryScriptURL ?? process.env.SUPERMETRY_SCRIPT_URL ?? "http://app.test:18090/js/missing.js",
  };
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: options.port ?? Number(process.env.SITE_PORT ?? 18091),
    fetch(request) {
      const { pathname } = new URL(request.url);
      if (pathname === "/files/report.pdf") {
        return new Response(readFileSync(join(root, "files/report.pdf")), {
          headers: { "content-type": "application/pdf", "content-disposition": 'attachment; filename="report.pdf"' },
        });
      }
      const filename = pathname.startsWith("/spa/") ? "spa.html" : routes.get(pathname);
      const status = filename ? 200 : 404;
      const page = readFileSync(join(root, filename ?? "404.html"), "utf8");
      return new Response(injectTrackers(page, config), {
        status,
        headers: { "content-type": "text/html; charset=utf-8", "cache-control": "no-store" },
      });
    },
  });
  return {
    url: server.url.origin,
    async stop() {
      await server.stop(true);
    },
  };
}

if (import.meta.main) {
  const site = startSite();
  console.log(`accuracy test site listening on ${site.url}`);
}
