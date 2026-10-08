type Upload = { text: string; trigger: string | null; page: string | null; sequence: string | null };

let uploads: Upload[] = [];
let starts = 0;

const root = import.meta.dir + "/../../../tracker";
const tracker = await Bun.file(root + "/tracker.full.min.js").text();

function fixture(pathname: string): Response {
  const replayConsent = pathname === "/consent-always" ? "always" : "never";
  const hugeDOM = pathname === "/trigger/huge" ? `<div>${"large-dom-text".repeat(440_000)}</div>` : "";
  return new Response(`<!doctype html>
    <html><body>
      ${hugeDOM}
      <input id="password" type="password">
      <input id="card" autocomplete="cc-number">
      <input id="ordinary" type="text">
      <script data-site="abc123def456" data-allow-localhost data-replay="${replayConsent}" src="/tracker.js"></script>
      <p id="ready">${pathname}</p>
    </body></html>`, { headers: { "Content-Type": "text/html" } });
}

Bun.serve({
  hostname: "127.0.0.1",
  port: 4177,
  async fetch(request) {
    const url = new URL(request.url);
    if (url.pathname === "/health") return new Response("ok");
    if (url.pathname === "/tracker.js") {
      return new Response(tracker, { headers: { "Content-Type": "application/javascript" } });
    }
    if (url.pathname === "/replay.js") {
      const file = Bun.file(import.meta.dir + "/../dist/replay.js");
      return file.exists() ? new Response(file, { headers: { "Content-Type": "application/javascript" } }) : new Response("missing", { status: 404 });
    }
    if (url.pathname === "/api/replay/start" && request.method === "POST") {
      starts++;
      const body = await request.json() as { u?: string };
      const mode = body.u?.includes("/trigger") ? "trigger" : "full";
      return Response.json({
        m: mode,
        mask: false,
        t: { e: true, r: true, u: body.u?.includes("/url-spa") ? ["*checkout*"] : [], x: !body.u?.includes("/manual-off") },
      });
    }
    if (url.pathname === "/api/replay/chunk" && request.method === "POST") {
      const compressed = new Uint8Array(await request.arrayBuffer());
      uploads.push({
        text: new TextDecoder().decode(Bun.gunzipSync(compressed)),
        trigger: url.searchParams.get("t"),
        page: url.searchParams.get("p"),
        sequence: url.searchParams.get("q"),
      });
      return new Response(null, { status: 202 });
    }
    if (url.pathname === "/test/reset" && request.method === "POST") {
      uploads = [];
      starts = 0;
      return new Response(null, { status: 204 });
    }
    if (url.pathname === "/test/state") return Response.json({ uploads, starts });
    if (["/full", "/trigger", "/save-data", "/consent-always", "/trigger/manual-off", "/trigger/url-spa", "/trigger/huge"].includes(url.pathname)) return fixture(url.pathname);
    return new Response("not found", { status: 404 });
  },
});
