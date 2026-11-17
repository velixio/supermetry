import * as Sentry from "@sentry/electron/renderer";
Sentry.init();
if (location.hash === "#renderer-exception") {
  setTimeout(() => {
    throw new Error("fixture renderer exception");
  }, 800);
}
