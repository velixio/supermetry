# Supermetry breaking rename

The Supermetry rename is an intentional breaking cutover. There are no legacy
configuration aliases or browser namespace shims.

Before upgrading an existing installation:

1. Rename every `PANEL_*` setting to its corresponding `SUPERMETRY_*` name. In
   particular, preserve the exact secret-key and database URL values; changing
   either value can make encrypted TOTP/notification data unreadable or point
   the service at a different database.
2. Update the executable, service, user/group, config and writable-data paths to
   the `supermetry` names used by the current release assets. The installer does
   not migrate an older installation automatically.
3. Replace tracker calls and markup with `window.supermetry`,
   `data-supermetry-event`, `data-supermetry-prop-*`, and
   `<meta name="supermetry:404">`. Reinstall the generated site snippet.
4. Expect users to sign in again: session, CSRF and share-cookie names changed.
   The opt-out storage key also changed to `supermetry_ignore`, so communicate
   the cutover and reapply opt-out choices where required by your policy.
5. Update API integrations for `X-Supermetry-Org`,
   `X-Supermetry-Privacy-*`, and `X-Supermetry-Signature`. Update npm imports to
   `@supermetry/tracker` and `@supermetry/api-client`.
6. Use `/app` as the dashboard entry. Keep `SUPERMETRY_BASE_URL` on the instance
   origin; `SUPERMETRY_HOMEPAGE_URL` is only the public “Powered by” link and
   defaults to `https://supermetry.com`.

The database migration history and stored analytics rows are not renamed or
reset. Encryption-key labels and the path-policy fingerprint seed now use the
`supermetry/...` names, so run the one-shot command once after the upgrade
and before starting the new release, with the same `SUPERMETRY_SECRET_KEY` and
`SUPERMETRY_TRACKING_PUBLIC_PATHS` as before:

```sh
supermetry migrate-labels
```

It re-encrypts TOTP secrets, alert channel secrets and the replay S3 secret
key, and rewrites stored path-policy IDs, all in one transaction. It is
idempotent and fails without changing anything if the secret key is wrong.
Path-policy rows written under a policy that is no longer configured cannot
be mapped and keep their old ID. Until the command has run, TOTP codes, alert
secrets and the stored S3 key will not decrypt.

Historical design specifications and benchmark result artifacts also retain the
names, commands, paths and output keys that were true when those records were
created. Current guides, examples and runtime configuration use Supermetry.
Back up the database and data directory before the cutover. The
repository is now `velixio/supermetry`.
