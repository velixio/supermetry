// Bundled into dist/client.js and loaded by the fixture page. Only init plus
// real page behaviour; the recorded cases make no manual capture calls, the
// "feedback" case (live check only) calls captureFeedback.
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
    case "feedback": {
      // User feedback sent by the SDK's own API, tied to a captured error.
      const eventId = Sentry.captureException(new Error("fixture feedback target"));
      Sentry.captureFeedback({
        message: "The fixture page froze, write to fixture-user@example.test",
        email: "fixture-user@example.test",
        name: "Fixture User",
        associatedEventId: eventId,
        url: "http://127.0.0.1/checkout?token=abc",
      });
      break;
    }
    case "session":
      break;
  }
};
