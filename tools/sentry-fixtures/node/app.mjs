// Usage: node --import @sentry/node/import app.mjs <case>
// Generator for @sentry/node fixtures. Only init plus real behaviour; the one
// manual call is captureMessage in the captured-message case.
import * as Sentry from "@sentry/node";

const which = process.argv[2];
const opts = {
  dsn: process.env.SENTRY_FIXTURE_DSN,
  release: "fixture@1.0.0",
  environment: "fixture",
  serverName: "fixture-host",
  tracesSampleRate: 0,
  // keep default integrations; rewriteFrames only strips this machine's absolute paths
  integrations: [Sentry.rewriteFramesIntegration({ root: process.cwd() })],
};
if (which === "client-report") {
  opts.beforeSend = () => null; // force a drop so a client report is produced
}
Sentry.init(opts);

const delay = (ms) => new Promise((r) => setTimeout(r, ms));

switch (which) {
  case "unhandled-exception":
    setTimeout(() => {
      throw new Error("fixture unhandled exception");
    }, 50);
    break;
  case "unhandled-rejection":
    Promise.reject(new Error("fixture unhandled rejection"));
    await delay(300);
    await Sentry.flush(2000);
    break;
  case "captured-message":
    Sentry.captureMessage("fixture captured message", "info");
    await Sentry.flush(2000);
    break;
  case "session-aggregates": {
    const http = await import("node:http");
    const server = http.createServer((req, res) => res.end("ok"));
    await new Promise((r) => server.listen(0, "127.0.0.1", r));
    const port = server.address().port;
    for (let i = 0; i < 3; i++) {
      await fetch(`http://127.0.0.1:${port}/`).then((r) => r.text());
    }
    server.close();
    await Sentry.close(2000);
    break;
  }
  case "client-report":
    // a dropped event via beforeSend; unhandled rejection is the trigger
    Promise.reject(new Error("fixture dropped"));
    await delay(300);
    await Sentry.close(2000);
    break;
  default:
    console.error("unknown case", which);
    process.exit(2);
}
