import { useMutation } from '@tanstack/react-query'

import { Button } from '@/components/ui/button'
import { CopyButton } from '@/features/settings/InstallRecipes'
import { privateRequest } from '@/features/settings/privateApi'

/**
 * Sentry's organization-token layout, so users set only SENTRY_AUTH_TOKEN: the CLI reads the server URL and org from the payload.
 * Sentry clients split on underscores and want a 43 character secret, so the secret is the upload key without its underscores.
 */
export function formatUploadToken(key: string, org: string, url: string, issuedAt = Math.floor(Date.now() / 1000)): string {
  const payload = JSON.stringify({ iat: issuedAt, url, region_url: url, org })
  const bytes = new TextEncoder().encode(payload)
  const encoded = btoa(String.fromCharCode(...bytes))
  return `sntrys_${encoded}_${key.replace(/^pnl_/, '').replace('_', '')}`
}

/** Issues an API key limited to the releases:write scope and this app, shown once in the sntrys_ format. */
export function UploadTokenSection({ org, orgId, siteId, origin = window.location.origin }: { org: string; orgId: number; siteId: number; origin?: string }) {
  const create = useMutation({
    mutationFn: () => privateRequest<{ id: number; key: string }>('POST', `/api/private/orgs/${orgId}/api-keys`, {
      orgId,
      body: { name: 'Source map upload', scopes: ['releases:write'], site_ids: [siteId] },
    }),
  })
  const token = create.data ? formatUploadToken(create.data.key, org, origin) : ''
  const usage = `SENTRY_AUTH_TOKEN=${token || '<your upload token>'} npx @sentry/cli sourcemaps upload ./dist`
  return (
    <section aria-labelledby="app-upload-title" className="flex min-w-0 flex-col gap-3">
      <h2 id="app-upload-title" className="text-lg font-semibold">Upload source maps</h2>
      <p className="text-sm text-muted-foreground">
        Readable JavaScript stack traces need your source maps. An upload token can only upload source maps and manage releases for this app. It works with <code>sentry-cli</code> and the Sentry bundler plugins; the token carries this server&apos;s address, so nothing else needs configuring.
      </p>
      {token ? (
        <>
          <p className="text-sm">Copy the token now. It is shown only once.</p>
          <pre className="overflow-auto rounded-lg bg-muted p-3 text-xs break-all whitespace-pre-wrap select-all" tabIndex={0} aria-label="Upload token"><code>{token}</code></pre>
          <CopyButton value={token} label="Copy upload token" />
          <pre className="overflow-auto rounded-lg bg-muted p-3 text-xs" tabIndex={0} aria-label="Upload command"><code>{usage}</code></pre>
        </>
      ) : (
        <div><Button type="button" variant="outline" disabled={create.isPending} onClick={() => create.mutate()}>Create upload token</Button></div>
      )}
      {create.error ? <p role="alert" className="text-sm text-destructive">{create.error.message}</p> : null}
    </section>
  )
}
