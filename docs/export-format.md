# Export format

Supermetry exports are plain files you can read without Supermetry. This page
describes the site export ZIP, its manifest and schemas, and the versioning
rules. Scheduled exports to S3 and the organization settings bundle are
described further down.

## Site export ZIP

`GET /api/private/sites/{site}/export?from=YYYY-MM-DD&to=YYYY-MM-DD` streams a
ZIP archive of CSV files (UTF-8, RFC 4180, header row first). Cells that could
be read as spreadsheet formulas are prefixed with a single quote. Dates are in
the site's time zone; timestamps are RFC 3339 UTC.

Which files appear depends on the site kind: web sites get the daily totals,
breakdown and custom-event files; app sites add the `app_*`, `metric_*` and
`logs.csv` files and an `app_attachments/` directory of raw attachment bytes.

### manifest.json

The last entry of every archive is `manifest.json`:

```json
{
  "schema_version": 1,
  "generated_at": "2026-11-18T09:30:00Z",
  "generator": "supermetry",
  "range": {"from": "2026-10-01", "to": "2026-10-31"},
  "site": {"id": 7, "public_id": "abcdefghijkl", "name": "Docs", "kind": "web", "timezone": "Europe/Berlin"},
  "files": [
    {"path": "daily_totals.csv", "format": "csv", "rows": 31, "bytes": 612,
     "sha256": "…", "columns": ["day", "visitors", "…"], "schema": "daily_totals.schema.json"}
  ]
}
```

- `rows` counts data rows without the header. `bytes` and `sha256` describe the
  uncompressed content of the entry, so you can verify an archive after
  extracting it: `sha256sum daily_totals.csv`.
- Files under `app_attachments/` have `format: "binary"` and no rows.
- Every file in the archive other than the manifest is listed.

### Schemas

Each CSV has a JSON Schema (draft 2020-12) describing one row, published in
[`docs/export-schemas/`](export-schemas/) together with `manifest.schema.json`.
Integers, numbers, booleans, dates and timestamps are typed; a column marked
optional uses `["type", "null"]` and is an empty cell in the CSV. Columns that
hold JSON documents are strings containing JSON. The schemas describe the CSV
columns in order (`x-supermetry-columns`); they are generated from the table
registry in `internal/siteexport/format.go`, and a test fails if a column is
added or renamed without regenerating them
(`UPDATE_EXPORT_SCHEMAS=1 go test ./internal/siteexport -run PublishedSchemas`).

## Versioning

`schema_version` is an integer. It increases when a file or column is added,
removed or changes meaning. Consumers should read the manifest first, refuse a
version they do not know, and match columns by header name rather than by
position. Version 1 is the first versioned format; its CSV files are unchanged
from earlier exports, and `manifest.json` is the only addition.

## Compression and Parquet

Archives are ZIP (deflate). Scheduled exports use the same ZIP so one reader
handles both. Parquet is not offered. The pure-Go library
`parquet-go/parquet-go` (Apache-2.0) adds only about 0.6 MB to the binary
(53.74 MB to 54.34 MB measured), but it brings five new modules and bumps
`google.golang.org/protobuf`, and no consumer has asked for it yet. CSV in a ZIP
loads into DuckDB, pandas and warehouses directly. Revisit it if a customer
needs columnar loads.

## Scheduled exports to S3-compatible storage

An owner or admin can schedule a site (or every site of the organization) to be
written daily, weekly or monthly (03:00 UTC) to any S3-compatible bucket: AWS S3,
Cloudflare R2, Backblaze B2, MinIO. Each run uploads one ZIP in the format above
to `<prefix>/site-<id>/supermetry-site-<id>-<from>-<to>.zip`.

- **Incremental** (default): the first run exports all history up to the last
  closed day in the site's time zone; later runs export only the days since the
  previous successful run, so each object covers a distinct range. Turn it off to
  write the full history each time.
- **Large exports** stream: the ZIP goes straight to a multipart upload (16 MiB
  parts), so neither the server nor the database buffers the whole file.
- **Credentials**: the secret is encrypted at rest with the instance secret key
  and is never returned by the API.
- **Safety**: the endpoint must be https and must not resolve to a private,
  loopback or link-local address. Self-hosters running a local MinIO set
  `SUPERMETRY_ALLOW_PRIVATE_EXPORT_ENDPOINTS=true`, which also allows http.
- **History and failures**: every attempt is recorded (status, days, bytes, object
  key, error). A failed schedule retries after six hours; owners and admins get
  one email when it starts failing, when mail is configured.

Grant the key only `s3:PutObject` (and `s3:AbortMultipartUpload`) on the prefix.
The Test action writes and deletes a small `.supermetry-export-check` object.

## Organization settings bundle

`GET /api/private/orgs/{org}/settings-export` downloads one JSON document
(`kind: "supermetry.org-settings"`, `schema_version: 1`) with: sites (name,
type, time zone, retention, allowed hosts, safe settings), goals, funnels,
segments, alerts, share-link settings and members (email, role). It never
contains secrets: no channel configuration or webhook URLs, no share-link
tokens or password hashes, no API keys, SMTP settings or credentials. Alerts
name their channels by kind and name; alert rules (which hold webhook targets)
are listed under `skipped`.

`POST /api/private/orgs/{org}/settings-import` recreates a bundle in another
organization. Use `?dry_run=true` first: the report lists what would be created.
Existing sites are left unchanged; share links are never recreated; alerts whose
channel does not exist in the target arrive disabled until you create a channel
of the same kind and name. Members are only listed unless `?invite_members=true`
is given, which sends invitations (never to owners).
