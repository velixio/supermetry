// Bundled into dist/client.js and loaded by the fixture page. Only init plus
// real page behaviour; no manual capture calls.
import * as Sentry from "@sentry/browser";

const which = location.hash.slice(1);
const opts = {
  dsn: window.__DSN__,
  release: "fixture@1.0.0",
  environment: "fixture",
};
if (which === "client-report") {
  opts.beforeSend = () => null;
}
Sentry.init(opts);

window.__run = async () => {
  switch (which) {
    case "onerror":
      setTimeout(() => {
        throw new Error("fixture onerror");
      }, 20);
      break;
    case "unhandledrejection":
      Promise.reject(new Error("fixture unhandledrejection"));
      break;
    case "breadcrumbs":
      document.getElementById("btn").click();
      await fetch("/ping").then((r) => r.text());
      setTimeout(() => {
        throw new Error("fixture breadcrumbs");
      }, 20);
      break;
    case "client-report":
      setTimeout(() => {
        throw new Error("fixture dropped");
      }, 20);
      break;
    case "session":
      break;
  }
};
