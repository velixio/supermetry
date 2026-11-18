# Observability: logs and metrics

App projects receive OpenTelemetry **logs** and **metrics** next to traces, and
Sentry SDK logs over the DSN. Everything lands in PostgreSQL; there is nothing
else to run. From an error you can open its trace and the logs written inside
that trace.

## Send data

### OpenTelemetry (any language)

Set the same two variables you use for traces. The SDK appends `/v1/traces`,
`/v1/logs` and `/v1/metrics` itself. **Settings, Installation** shows the values
for your project.

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=https://<your host>/api/<project id>/otlp
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_EXPORTER_OTLP_HEADERS=authorization=Bearer <dsn public key>
```

Both `http/protobuf` and `http/json` work, gzip is accepted. Most SDKs only
enable the logs and metrics exporters when you ask for them, for example
`OTEL_LOGS_EXPORTER=otlp` and `OTEL_METRICS_EXPORTER=otlp`.

### Sentry logs

Sentry SDKs that support structured logs send them over the DSN:

```js
Sentry.init({ dsn: "https://<public key>@<your host>/<project id>", enableLogs: true });
```

## Search logs

Open **Logs** in the sidebar. Terms are separated by spaces and all must match.

| Term | Matches |
|---|---|
| `timeout` or `"connection reset"` | The word or phrase in the log text, case-insensitive, at least 3 characters |
| `severity:error` | Exactly this severity: trace, debug, info, warn, error, fatal |
| `severity:>=warn` | At or above; `>`, `<=` and `<` work too |
| `service:api`, `env:production`, `release:1.4.0` | Exact match |
| `trace:<32 hex>` and `span:<16 hex>` | One trace or span |
| `attr.http.route:/pay` | An attribute value |
| `!service:worker` | Any term with a leading `!` is excluded |

A query has at most 12 terms and 300 characters. A mistake in the query is shown
with the character position. Results are newest first, 100 per page, and the
window defaults to 24 hours (at most 30 days, never older than the log
retention).

## Metrics

**Metrics** in the sidebar charts any metric your app sends: pick a metric, an
aggregation, up to two group-by attributes and filters. Gauges, sums and
histograms are supported. Cumulative counters and histograms are turned into
deltas on arrival, so a restart of your app or of Supermetry never shows as a
spike. Summaries and exponential histograms are counted and dropped.

## Limits

| Limit | Value |
|---|---|
| Request size | 4 MiB compressed, 20 MiB decompressed |
| Records per request | 10,000 log records or 10,000 metric data points (413 above) |
| Log body | 8,192 bytes (cut, marked truncated) |
| Log attributes | 32 per record, key 128 bytes, string value 1,024 bytes, 4,096 bytes encoded in all |
| Metric name | 255 bytes, `^[A-Za-z][A-Za-z0-9_.\-/]*$` |
| Metric attributes | 16 per series, value 256 bytes |
| Series per project | 10,000 (new series beyond it are dropped and counted) |
| Histogram buckets | 64 |
| Daily quota | 5,000,000 log records and 50,000,000 metric points per UTC day, per project |

Over the daily quota the server answers 200 with an OTLP `partial_success` and
the message "daily quota reached", which SDKs do not retry. A full queue answers
503 with `Retry-After: 5`. Owners and admins change the log retention and both
quotas under **Settings, App**; `0` for a quota means unlimited.

## Retention

Raw logs are kept 7 days by default (1 to 30, per project). Raw metric points
are kept 14 days and hourly metric rollups 90 days. Data is removed by dropping
whole daily partitions, and queries hide anything older than the project's own
retention even when the partition still exists.

## What is scrubbed

- Log bodies and string attribute values pass the same personal-data scrubber as
  error events (emails, tokens and similar patterns).
- Attributes whose key looks sensitive (authorization, cookie, password, secret,
  token and the like) are dropped.
- IP attributes (`client.address`, `net.peer.ip`, `net.sock.peer.addr`,
  `http.client_ip`, `source.address`), `user.email`, `user.name`,
  `user.full_name` and `enduser.*` other than `enduser.id` are dropped.
- `user.id` and `enduser.id` are replaced by a salted hash and never shown;
  erasing a user in app settings also deletes their log rows.

## From error to logs

Open an error event: when the SDK recorded a trace id, **View trace** opens the
waterfall, whose **Logs** tab lists the logs of that trace oldest first, and
**View logs** opens the Logs page filtered to that trace. Logs link back to
their trace from the search results.

## Errors from logs

OTLP log records also create issues, so an app that only writes logs gets
error tracking without an SDK. A record becomes an issue when its severity is
ERROR or above (OTLP severity 17 and up; FATAL shows as level fatal), or when
it has no severity but carries `exception.type` or `exception.message`. WARN
and lower never do. Records group by exception type and message (with a
stack trace from `exception.stacktrace` when present), or by the message with
numbers normalised, and the issue shows "From logs" as its source.

- One logs request creates at most 100 issue events; the rest are counted as
  `log_issue_capped` in the drop counters. The request itself still succeeds.
- A record over the daily logs quota is not stored in Logs, but its issue is
  still created. Issues are limited only by the key's event budget and the
  app's error quota.
- When a log record and a span event describe the same exception and carry the
  same trace and span id, they land in one issue as a single occurrence.
