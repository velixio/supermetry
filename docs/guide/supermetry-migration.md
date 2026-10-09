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
reset. The historical `panel/.../v1` cryptographic derivation labels and path
policy fingerprint seed are intentionally retained as persisted data-format
identifiers; changing them would make encrypted values unreadable or invalidate
stored provenance. They are not configuration aliases or public namespaces.
Back up the database and data directory before the cutover. Existing
`velixio/panel` repository links remain valid until a separate repository rename
is authorized and completed.
