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
handles both. Parquet is not offered: see the project notes for the measured
cost.
