# Sending reports from desktop and mobile apps

Desktop apps (Electron, Tauri, native), mobile apps and backend services can send
events, including failure reports, to the public events endpoint that the web
tracker uses. This guide lists the rules that decide whether a report is stored.
Breaking most of them gives no error back: the request still gets `202 Accepted`,
and the report is only counted on the **Data health** page.

A dedicated app intake (an app site kind, an ingest key, crash grouping and
crash-free rates) is planned. Until it ships, use the rules below.

## Endpoint

```
POST /api/public/v1/events
Content-Type: application/json
```

```json
{
  "site": "<site ID>",
  "name": "diagnostic_report",
  "url": "https://app.example.com/diagnostics",
  "id": "3f2a9c0e8b7d4e6fa1c2d3e4f5a6b7c8",
  "ts": 1763200000000,
  "profile_id": "install_0123456789abcdef",
  "props": {
    "category": "app_crash",
    "app_version": "1.2.3",
    "platform": "macos",
    "arch": "arm64",
    "error_type": "TypeError"
  }
}
```

Limits: a body of at most 8 KB, at most 30 props, prop keys of at most 300
bytes and values of at most 2000 bytes.

## Rules

1. **Send your own User-Agent**, for example `MyApp/1.2.3 (macos; arm64)`.
   Headless browsers and well-known crawler and HTTP-client agents are treated
   as bots and dropped. That includes Electron's default agent, which contains
   `Electron/`, and empty agents. In Electron, set the agent on the request, or
   send reports from the main process with a custom header.
2. **Add the reporting host to the site's allowed hosts.** The host in `url`
   must be listed under **Allowed hosts** in the site settings. If it isn't,
   the request is rejected with `400`.
3. **Turn profiles on if you need props.** In anonymous tracking mode, or with
   profiles off for the site, props are removed before storing. The report then
   arrives without its details.
4. **Give each report a stable `id`.** The id is 1 to 64 characters of
   `A–Z a–z 0–9 _ -`. If a retry sends the same id, the copy is ignored.
5. **Send queued reports within 24 hours.** `ts` is the time the event
   happened, in Unix milliseconds. Reports more than 24 hours old are dropped
   and appear on the **Data health** page as **Too old**. A timestamp more than five
   minutes in the future is replaced by the time the report was received, and
   counted as clock skew.
6. **Keep personal data out.** Don't send user names, email addresses or file
   paths that contain home folders. Strip them from stack frames before
   sending.

## Checking delivery

The **Data health** page lists every dropped event by reason: bot user agent, host
not allowed, too old, clock skew, rate limited, and so on. If reports go
missing, look there first.
