# Collect errors without changing the app

Some apps cannot or should not get an SDK: services you do not own, binaries you
cannot rebuild, or a fleet where touching every repo is not worth it. Supermetry
takes OpenTelemetry, so a few lines of configuration next to the app can report
its errors. Nothing in the app's source changes.

| Route | Works for | You get |
|---|---|---|
| Collector reading the systemd journal | Go services (or anything) logging to journald | Logs, and Issues from ERROR lines and Go panics |
| `opentelemetry-instrument` wrapper | Python apps (FastAPI, Flask, Django) in Docker | Traces, Issues from exceptions and ERROR logs, and logs |

Create an App project first and copy its endpoint and DSN public key from
**Settings, Installation**. Examples below use placeholders; ready-to-copy files
live in [`deploy/examples/zero-code/`](../../deploy/examples/zero-code/).

## Services logging to the systemd journal

An OpenTelemetry Collector (contrib build) on the host reads the journal of the
units you name, joins multi-line entries, and sends the result to
`/api/<project id>/otlp/v1/logs`. Go `log/slog` output is understood in both
text (`time=... level=ERROR msg=...`) and JSON form. A Go panic arrives as one
record, severity FATAL, with the frames as its stack trace, so one panic is one
issue, not one issue per line. Lines logged at ERROR or above become
[Issues](observability.md#errors-from-logs); lower levels are plain logs.

1. Install the `otelcol-contrib` package (deb or rpm) from the
   [collector releases](https://github.com/open-telemetry/opentelemetry-collector-releases/releases).
2. Let it read the journal, and restart it:

   ```sh
   sudo usermod -aG systemd-journal otelcol-contrib
   ```

3. Copy `otelcol-journald.yaml` to `/etc/otelcol-contrib/config.yaml` and edit
   the `units:` list to your services.
4. Create `/etc/otelcol-contrib/supermetry.env` (mode 0600, owned by root):

   ```sh
   SUPERMETRY_OTLP_ENDPOINT=https://<your host>/api/<project id>/otlp
   SUPERMETRY_DSN_KEY=<dsn public key>
   ```

5. Load it into the service and start it:

   ```sh
   sudo systemctl edit otelcol-contrib   # add: [Service]
                                         #      EnvironmentFile=/etc/otelcol-contrib/supermetry.env
   sudo systemctl enable --now otelcol-contrib
   ```

To check it: open **Logs** and filter `service.name`, which is the unit name
without `.service`. An ERROR line from one of the units shows up under
**Issues** within about 10 seconds.

Notes:

- The collector is capped at 128 MiB (`memory_limiter`). Keep that much RAM free.
- Only lines that parse as slog (or a panic) get a severity. Plain-text lines
  without a level are sent as logs with no severity and never become issues.
- A line that is not the start of a new entry is appended to the previous one
  from the same unit, so unrecognised multi-line output stays together.
- The journal cursor is kept in `/var/lib/otelcol-contrib/storage`, so a
  restart neither replays nor loses lines.
- To undo it: `sudo systemctl disable --now otelcol-contrib`.

To test a changed config, run `deploy/examples/zero-code/check.sh`. It validates
the file and replays a fixture (logfmt, JSON, a 12-line panic) through the same
parsing.
