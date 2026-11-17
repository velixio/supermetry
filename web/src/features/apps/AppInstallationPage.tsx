import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useMemo } from 'react'

import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { CopyButton } from '@/features/settings/InstallRecipes'
import { privateRequest } from '@/features/settings/privateApi'

import { dsnOf, DsnKeys, PrimaryDsn, useDsnKeys } from './DsnKeys'
import { dsnPlaceholder, sdkSnippets } from './sdkSnippets'
import { TestReportCheck } from './TestReportCheck'

type IngestKey = { prefix: string; created_at: string; expires_at: string | null }

const sdkURL = 'https://github.com/velixio/supermetry/tree/main/sdk'
const migrateURL = 'https://github.com/velixio/supermetry/blob/main/docs/guide/migrate-from-sentry.md'
const guideURL = 'https://github.com/velixio/supermetry/blob/main/docs/guide/apps.md'
const keyPlaceholder = '<your key>'

/** Setup code for each client kit, with this app's ID and host filled in. The key stays a placeholder: it is shown only once. */
export function appInstallSnippets(appId: string, host: string) {
  const typescript = `import { init, startSession, captureException, flush } from "@supermetry/app-health";

init({
  appId: "${appId}",
  ingestKey: "${keyPlaceholder}",
  host: "${host}",
  release: "1.0.0",                // your app's version
  environment: "production",
});
startSession();                    // once per run: powers crash-free sessions

// Node and the Electron main process:
process.on("uncaughtException", async (err) => {
  captureException(err, { handled: false, level: "fatal" });
  await flush(2000);
  process.exit(1);
});

// Browsers and the Electron renderer use this instead:
// window.addEventListener("error", (e) => captureException(e.error, { handled: false }));`
  const python = `import supermetry_apps as sm

sm.init(
    app_id="${appId}",
    ingest_key="${keyPlaceholder}",
    host="${host}",
    release="1.0.0",             # your app's version
    environment="production",
)
sm.start_session()               # once per run: powers crash-free sessions

try:
    risky()
except Exception as err:
    sm.capture_exception(err, handled=True)

sm.flush(2.0)                    # before exit`
  const http = `POST ${host}/api/public/v1/app-events
Content-Type: application/json
X-Supermetry-Key: ${keyPlaceholder}

{
  "site": "${appId}",
  "events": [
    { "id": "<random 32 hex>", "kind": "crash", "release": "1.0.0",
      "platform": "linux", "error_type": "SegFault", "frames": "main.c:42 render" }
  ]
}`
  return { typescript, python, http }
}

/** How to get each kit into a project; neither is on npm or PyPI yet. */
const kitInstall = {
  typescript: `git clone --depth 1 https://github.com/velixio/supermetry
cd supermetry/sdk/typescript
npm install && npm run build && npm pack
# then, in your app:
npm install /path/to/supermetry/sdk/typescript/supermetry-app-health-*.tgz`,
  python: 'pip install "git+https://github.com/velixio/supermetry#subdirectory=sdk/python"',
} as const

const kits = [
  { value: 'typescript', label: 'TypeScript', note: 'Node 18+, Electron, Bun, Deno and browsers.', folder: 'typescript', install: kitInstall.typescript },
  { value: 'python', label: 'Python', note: 'Python 3.9 and newer.', folder: 'python', install: kitInstall.python },
  { value: 'http', label: 'HTTP', note: 'Any other language: up to 20 events and 16 KB per request.', folder: '', install: '' },
] as const

export function AppInstallationPage({ org, site, orgId, siteId, publicId, timezone }: { org: string; site: string; orgId: number; siteId: number; publicId: string; timezone: string }) {
  const host = window.location.origin
  const snippets = useMemo(() => appInstallSnippets(publicId, host), [publicId, host])
  const dsnKeys = useDsnKeys(orgId, siteId)
  const primary = dsnKeys.data?.keys[0]
  const dsn = primary ? dsnOf(primary, siteId) : dsnPlaceholder
  const sdks = useMemo(() => sdkSnippets(dsn), [dsn])
  const keys = useQuery({
    queryKey: ['app-ingest-keys', siteId],
    queryFn: () => privateRequest<{ keys: IngestKey[] }>('GET', `/api/private/sites/${siteId}/ingest-keys`, { orgId }),
  })
  const fields = [
    { label: 'App ID', value: publicId, copy: 'Copy app ID' },
    { label: 'Host', value: host, copy: 'Copy host' },
  ]
  return (
    <div className="flex min-w-0 flex-col gap-8">
      <section aria-labelledby="app-dsn-title" className="flex min-w-0 flex-col gap-3">
        <h2 id="app-dsn-title" className="text-lg font-semibold">Your DSN</h2>
        <p className="text-sm text-muted-foreground">
          Point any Sentry SDK at Supermetry by changing its DSN. The DSN is public by design: it can only send reports, so it is safe to ship inside your app.
        </p>
        {dsnKeys.isPending ? <p role="status" className="text-sm text-muted-foreground">Loading DSN…</p> : null}
        {dsnKeys.isError ? <p role="alert" className="text-sm text-destructive">{dsnKeys.error.message}</p> : null}
        {primary ? <PrimaryDsn dsn={dsn} /> : null}
        {dsnKeys.isSuccess && !primary ? <p className="text-sm text-muted-foreground">No DSN key yet. Create one below.</p> : null}
      </section>

      <section aria-labelledby="app-sdk-title" className="flex min-w-0 flex-col gap-3">
        <h2 id="app-sdk-title" className="text-lg font-semibold">Add your SDK</h2>
        <p className="text-sm text-muted-foreground">
          Use the official Sentry SDK for your platform with its default settings. Only the DSN and the release change. Set <code>release</code>: Sentry SDKs send no sessions without it.
        </p>
        <Tabs defaultValue="browser" className="min-w-0">
          <TabsList className="h-auto flex-wrap justify-start">
            {sdks.map((sdk) => <TabsTrigger key={sdk.value} value={sdk.value}>{sdk.label}</TabsTrigger>)}
          </TabsList>
          {sdks.map((sdk) => (
            <TabsContent key={sdk.value} value={sdk.value} className="flex min-w-0 flex-col gap-3">
              <p className="text-sm text-muted-foreground">{sdk.note}</p>
              <pre className="max-h-96 overflow-auto rounded-lg bg-muted p-3 text-xs" tabIndex={0} aria-label={`${sdk.label} setup`}><code>{sdk.code}</code></pre>
              <CopyButton value={sdk.code} label={`Copy ${sdk.label} code`} />
            </TabsContent>
          ))}
        </Tabs>
        <p className="text-sm text-muted-foreground">
          Coming from Sentry or GlitchTip? See the{' '}
          <a className="font-medium text-foreground underline" href={migrateURL} target="_blank" rel="noreferrer">migration guide</a>.
        </p>
      </section>

      <section aria-labelledby="app-keys-title" className="flex min-w-0 flex-col gap-3">
        <h2 id="app-keys-title" className="text-lg font-semibold">DSN keys</h2>
        {dsnKeys.isSuccess ? <DsnKeys orgId={orgId} siteId={siteId} keys={dsnKeys.data.keys} /> : null}
      </section>

      <section aria-labelledby="app-check-title" className="flex min-w-0 flex-col gap-3">
        <h2 id="app-check-title" className="text-lg font-semibold">Check it works</h2>
        {/* Keyed by app: switching apps restarts the check instead of keeping the last app's result. */}
        <TestReportCheck key={siteId} appId={publicId} siteId={siteId} timezone={timezone} />
      </section>


      <details className="group min-w-0 rounded-lg border p-4">
        <summary className="cursor-pointer text-lg font-semibold">Legacy kits</summary>
        <div className="mt-4 flex min-w-0 flex-col gap-8">
          <p className="text-sm text-muted-foreground">The TypeScript and Python kits and the HTTP API still work. New projects should use a Sentry SDK above.</p>
        <section aria-labelledby="app-details-title" className="flex min-w-0 flex-col gap-3">
          <h2 id="app-details-title" className="text-lg font-semibold">Your app details</h2>
          <p className="text-sm text-muted-foreground">
            Every report needs all three: the app ID and host go in the kit&apos;s options, and the ingest key goes in the <code>X-Supermetry-Key</code> header. The key can only send reports, so it is safe to ship inside your app.
          </p>
          <dl className="grid min-w-0 gap-3 sm:grid-cols-2">
            {fields.map((field) => (
              <div key={field.label} className="flex min-w-0 flex-col gap-2 rounded-lg border p-3">
                <dt className="text-sm font-medium">{field.label}</dt>
                <dd className="min-w-0 break-all rounded-md bg-muted px-3 py-2 font-mono text-sm select-all" aria-label={field.label}>{field.value}</dd>
                <dd><CopyButton value={field.value} label={field.copy} /></dd>
              </div>
            ))}
            <div className="flex min-w-0 flex-col gap-2 rounded-lg border p-3 sm:col-span-2">
              <dt className="text-sm font-medium">Ingest key</dt>
              <dd className="flex flex-wrap gap-2 font-mono text-sm">
                {(keys.data?.keys ?? []).map((key) => (
                  <span key={key.prefix} className="flex items-center gap-2">
                    <code className="rounded-md bg-muted px-3 py-2">{`smk_${key.prefix}…`}</code>
                    {key.expires_at ? <span className="font-sans text-xs text-muted-foreground">valid until {new Date(key.expires_at).toLocaleDateString()}</span> : null}
                  </span>
                ))}
                {keys.isSuccess && keys.data.keys.length === 0 ? <span className="font-sans text-muted-foreground">No active key.</span> : null}
              </dd>
              <dd className="text-sm text-muted-foreground">
                The full key is shown only once, when the app is created or its key is rotated. Lost it?{' '}
                <Link className="font-medium text-foreground underline" to="/app/$org/$site/settings/app" params={{ org, site }}>Rotate the key in App settings</Link>
                . The old key keeps working for 7 days.
              </dd>
            </div>
          </dl>
        </section>

        <section aria-labelledby="app-kit-title" className="flex min-w-0 flex-col gap-3">
          <h2 id="app-kit-title" className="text-lg font-semibold">Add the client kit</h2>
          <p className="text-sm text-muted-foreground">
            The kits have no runtime dependencies and are MIT licensed. They are not on npm or PyPI yet: install them from{' '}
            <a className="font-medium text-foreground underline" href={sdkURL} target="_blank" rel="noreferrer">sdk/ in the Supermetry repository</a>
            . They never send exception messages. Details are in the{' '}
            <a className="font-medium text-foreground underline" href={guideURL} target="_blank" rel="noreferrer">apps guide</a>.
          </p>
          <Tabs defaultValue="typescript" className="min-w-0">
            <TabsList>
              {kits.map((kit) => <TabsTrigger key={kit.value} value={kit.value}>{kit.label}</TabsTrigger>)}
            </TabsList>
            {kits.map((kit) => (
              <TabsContent key={kit.value} value={kit.value} className="flex min-w-0 flex-col gap-3">
                <p className="text-sm text-muted-foreground">
                  {kit.note}
                  {kit.folder ? <> Kit: <a className="underline" href={`${sdkURL}/${kit.folder}`} target="_blank" rel="noreferrer"><code>sdk/{kit.folder}</code></a>.</> : null}
                </p>
                {kit.install ? (
                  <>
                    <h3 className="text-sm font-medium">Install</h3>
                    <pre className="overflow-auto rounded-lg bg-muted p-3 text-xs" tabIndex={0} aria-label={`${kit.label} install`}><code>{kit.install}</code></pre>
                    <CopyButton value={kit.install} label={`Copy ${kit.label} install`} />
                    <h3 className="text-sm font-medium">Set up</h3>
                  </>
                ) : null}
                <pre className="max-h-96 overflow-auto rounded-lg bg-muted p-3 text-xs" tabIndex={0} aria-label={`${kit.label} setup`}><code>{snippets[kit.value]}</code></pre>
                <CopyButton value={snippets[kit.value]} label={`Copy ${kit.label} code`} />
              </TabsContent>
            ))}
          </Tabs>
        </section>
        </div>
      </details>
    </div>
  )
}
