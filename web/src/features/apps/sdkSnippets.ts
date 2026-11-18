/** Per-SDK setup for unmodified Sentry SDKs: only the DSN and the release are set. */
export const RELEASE_SENTENCE = 'Sentry SDKs send sessions only when `release` is set in `Sentry.init`. Without it, crash-free rates fall back to error events.'

export type SdkSnippet = { value: string; label: string; note: string; code: string }

/** The DSN placeholder shown before a key exists. */
export const dsnPlaceholder = 'https://<public key>@<host>/<project id>'

export function sdkSnippets(dsn: string): SdkSnippet[] {
  return [
    {
      value: 'browser', label: 'JavaScript (browser)', note: 'npm install @sentry/browser',
      code: `import * as Sentry from "@sentry/browser";

Sentry.init({
  dsn: "${dsn}",
  release: "my-app@1.0.0", // your app's version
});`,
    },
    {
      value: 'node', label: 'Node', note: 'npm install @sentry/node',
      code: `const Sentry = require("@sentry/node");

Sentry.init({
  dsn: "${dsn}",
  release: "my-app@1.0.0", // your app's version
});`,
    },
    {
      value: 'electron', label: 'Electron', note: 'npm install @sentry/electron. Call init in the main process, before app ready.',
      code: `const Sentry = require("@sentry/electron/main");

Sentry.init({
  dsn: "${dsn}",
  release: "my-app@1.0.0", // your app's version
});`,
    },
    {
      value: 'python', label: 'Python', note: 'pip install sentry-sdk',
      code: `import sentry_sdk

sentry_sdk.init(
    dsn="${dsn}",
    release="my-app@1.0.0",  # your app's version
)`,
    },
    {
      value: 'go', label: 'Go', note: 'go get github.com/getsentry/sentry-go',
      code: `err := sentry.Init(sentry.ClientOptions{
    Dsn:     "${dsn}",
    Release: "my-app@1.0.0", // your app's version
})`,
    },
    {
      value: 'android', label: 'Android', note: 'Add io.sentry:sentry-android to your Gradle dependencies.',
      code: `SentryAndroid.init(this) { options ->
    options.dsn = "${dsn}"
    options.release = "my-app@1.0.0" // your app's version
}`,
    },
    {
      value: 'cocoa', label: 'Cocoa (iOS, macOS)', note: 'Add the Sentry package with Swift Package Manager or CocoaPods.',
      code: `SentrySDK.start { options in
    options.dsn = "${dsn}"
    options.releaseName = "my-app@1.0.0" // your app's version
}`,
    },
  ]
}

/** The OTLP/HTTP traces endpoint of a project: exporters append `/v1/traces` themselves. */
export function otlpEndpoint(origin: string, siteId: number): string {
  return `${origin}/api/${siteId}/otlp`
}

/** Environment variables for any OpenTelemetry SDK exporting over OTLP/HTTP. */
export function otelEnvSnippet(endpoint: string, key: string): string {
  return `OTEL_EXPORTER_OTLP_ENDPOINT=${endpoint}
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_EXPORTER_OTLP_HEADERS=authorization=Bearer ${key}`
}

/** Turning on transactions in Sentry SDKs that already have the DSN; 1.0 sends every transaction, so lower it for busy apps. */
export function tracingSnippets(): { value: string; label: string; code: string }[] {
  return [
    { value: 'browser', label: 'JavaScript (browser, Node)', code: 'Sentry.init({\n  // dsn and release as above\n  tracesSampleRate: 1.0,\n});' },
    { value: 'python', label: 'Python', code: 'sentry_sdk.init(\n    # dsn and release as above\n    traces_sample_rate=1.0,\n)' },
    { value: 'go', label: 'Go', code: 'sentry.Init(sentry.ClientOptions{\n    // Dsn and Release as above\n    EnableTracing:    true,\n    TracesSampleRate: 1.0,\n})' },
    { value: 'android', label: 'Android', code: 'options.tracesSampleRate = 1.0' },
    { value: 'cocoa', label: 'Cocoa (iOS, macOS)', code: 'options.tracesSampleRate = 1.0' },
  ]
}
