// Usage: xvfb-run electron . --no-sandbox <case>
// Cases: main-exception, renderer-exception, native-crash-1 (crashes), native-crash-2 (restart; sends minidump),
// consent-denied-1/-2 (the same with beforeSend returning null).
// Only init plus real behaviour; no manual capture calls.
const { app, BrowserWindow } = require("electron");
const path = require("node:path");
const Sentry = require("@sentry/electron/main");

const which = process.argv[process.argv.length - 1];
if (process.env.FIXTURE_USERDATA) app.setPath("userData", process.env.FIXTURE_USERDATA);

const denied = which.startsWith("consent-denied");
Sentry.init({
  // consent-denied: the app consent gate drops every event, native crashes included.
  ...(denied ? { beforeSend: () => null } : {}),
  dsn: process.env.SENTRY_FIXTURE_DSN,
  release: "fixture@1.0.0",
  environment: "fixture",
  serverName: "fixture-host",
});

app.whenReady().then(() => {
  if (which === "main-exception") {
    setTimeout(() => {
      throw new Error("fixture main exception");
    }, 500);
    setTimeout(() => app.quit(), 5000);
    return;
  }
  const win = new BrowserWindow({
    show: false,
    webPreferences: { preload: path.join(__dirname, "preload.js"), contextIsolation: true, sandbox: false },
  });
  win.loadFile("index.html", { hash: which });
  if (which === "native-crash-1" || which === "consent-denied-1") {
    setTimeout(() => process.crash(), 1500);
  } else {
    setTimeout(() => app.quit(), 6000);
  }
});
