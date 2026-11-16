import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useMemo } from 'react'

import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { CopyButton } from '@/features/settings/InstallRecipes'
import { privateRequest } from '@/features/settings/privateApi'

import { TestReportCheck } from './TestReportCheck'

type IngestKey = { prefix: string; created_at: string; expires_at: string | null }

const sdkURL = 'https://github.com/velixio/supermetry/tree/main/sdk'
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

process.on("uncaughtException", async (err) => {
  captureException(err, { handled: false, level: "fatal" });
  await flush(2000);
  process.exit(1);
});`
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

const kits = [
  { value: 'typescript', label: 'TypeScript', note: 'Node 18+, Electron, Bun, Deno and browsers.', folder: 'typescript' },
  { value: 'python', label: 'Python', note: 'Python 3.9 and newer.', folder: 'python' },
  { value: 'http', label: 'HTTP', note: 'Any other language: up to 20 events and 16 KB per request.', folder: '' },
] as const

export function AppInstallationPage({ org, site, orgId, siteId, publicId, timezone }: { org: string; site: string; orgId: number; siteId: number; publicId: string; timezone: string }) {
  const host = window.location.origin
  const snippets = useMemo(() => appInstallSnippets(publicId, host), [publicId, host])
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
          The kits have no runtime dependencies and are MIT licensed: copy the folder from{' '}
          <a className="font-medium text-foreground underline" href={sdkURL} target="_blank" rel="noreferrer">sdk/ in the Supermetry repository</a>{' '}
          into your project. They never send exception messages. Details are in the{' '}
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
              <pre className="max-h-96 overflow-auto rounded-lg bg-muted p-3 text-xs" tabIndex={0} aria-label={`${kit.label} setup`}><code>{snippets[kit.value]}</code></pre>
              <CopyButton value={snippets[kit.value]} label={`Copy ${kit.label} code`} />
            </TabsContent>
          ))}
        </Tabs>
      </section>

      <section aria-labelledby="app-check-title" className="flex min-w-0 flex-col gap-3">
        <h2 id="app-check-title" className="text-lg font-semibold">Check it works</h2>
        <TestReportCheck appId={publicId} siteId={siteId} timezone={timezone} />
      </section>
    </div>
  )
}
