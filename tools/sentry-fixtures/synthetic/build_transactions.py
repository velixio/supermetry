"""Builds the synthetic transaction and span fixtures (run: python3 -I build_transactions.py <testdata dir>).

No browser SDK capture or mobile toolchain is available for these shapes, so
they are hand-built from the public Sentry event and envelope protocol docs
(develop.sentry.dev/sdk/data-model/event-payloads/transaction, .../envelopes)
and the SDK behaviour recorded in .workspace/research/0a-sentry-sdks-js-python.md.
Every sidecar carries "synthetic": true; the span v2 container field mapping
is inferred, not captured. All data is invented (example.com hosts, fixture ids).
The Python case is recorded from sentry-sdk and lives beside it.
"""
import json
import os
import sys

out_dir = os.path.join(sys.argv[1], "transactions")
os.makedirs(out_dir, exist_ok=True)
KEY = "0123456789abcdef0123456789abcdef"
TRACE = "5f1c0a9e7b3d4c2a8e6f1d0b9a8c7e6d"
# 2026-11-17T13:58:00Z, a little before the clock the tests use (14:00Z).
T0 = 1794923880.0
SENT = "2026-11-17T13:58:05Z"


def compact(o):
    return json.dumps(o, separators=(",", ":"))


def write(name, header, items, sdk, notes):
    parts = [compact(header)]
    for item_header, payload in items:
        body = compact(payload)
        h = dict(item_header)
        h["length"] = len(body.encode())
        parts += [compact(h), body]
    with open(os.path.join(out_dir, name + ".envelope"), "w") as f:
        f.write("\n".join(parts) + "\n")
    ua = "sentry.%s/fixture" % sdk
    side = {
        "method": "POST", "path": "/api/42/envelope/", "query": "",
        "headers": {
            "Content-Type": "application/x-sentry-envelope",
            "X-Sentry-Auth": "Sentry sentry_version=7,sentry_client=%s,sentry_key=%s" % (ua, KEY),
            "User-Agent": ua,
        },
        "sdk": sdk, "sdk_version": "", "synthetic": True, "notes": "SYNTHETIC. " + notes,
    }
    with open(os.path.join(out_dir, name + ".json"), "w") as f:
        f.write(json.dumps(side, indent=2) + "\n")


def span(i, parent, op, desc, off, dur, data=None):
    s = {"trace_id": TRACE, "span_id": "%016x" % (0x1000 + i), "parent_span_id": parent, "op": op,
         "description": desc, "start_timestamp": T0 + off, "timestamp": T0 + off + dur, "status": "ok"}
    if data:
        s["data"] = data
    return s


def tx_event(event_id, name, source, op, root, spans, measurements, platform, extra=None):
    ev = {
        "type": "transaction", "event_id": event_id, "transaction": name, "transaction_info": {"source": source},
        "start_timestamp": T0, "timestamp": T0 + 1.5, "platform": platform,
        "release": "com.example.fixture@1.0.0+1", "environment": "fixture",
        "contexts": {"trace": {"trace_id": TRACE, "span_id": root, "op": op, "status": "ok"}},
        "spans": spans, "measurements": measurements,
    }
    ev.update(extra or {})
    return ev


ROOT = "%016x" % 0x1000

# --- JavaScript (traceLifecycle: 'static'): classic transaction item ---
spans = [
    span(1, ROOT, "browser.request", "GET /users/1234", 0.05, 0.3),
    span(2, ROOT, "resource.script", "https://app.example.com/assets/index-3f2a9c.js", 0.1, 0.4),
    span(3, ROOT, "http.client", "GET https://api.example.com/v1/me?access_token=fixture-token", 0.6, 0.25,
         {"http.response.status_code": 200}),
]
write("javascript-static", {"event_id": "a1" * 16, "sent_at": SENT, "sdk": {"name": "sentry.javascript.browser", "version": "11.13.0"}},
      [({"type": "transaction"}, tx_event("a1" * 16, "/users/1234", "url", "pageload", ROOT, spans,
                                           {"ttfb": {"value": 120.5, "unit": "millisecond"}, "lcp": {"value": 980.0, "unit": "millisecond"}}, "javascript"))],
      "javascript", "Browser pageload transaction as sent with traceLifecycle 'static' (and every SDK before v11): classic transaction item, source url so the name is parameterised.")

# --- JavaScript v11 default (traceLifecycle: 'stream'): span v2 container ---
def attr(v):
    return {"type": "string", "value": v}


def v2(i, parent, name, off, dur, segment=False, **attrs):
    s = {"trace_id": TRACE, "span_id": "%016x" % (0x2000 + i), "name": name, "status": "ok", "is_segment": segment,
         "start_timestamp": T0 + off, "end_timestamp": T0 + off + dur,
         "attributes": {k.replace("_", ".", 1) if k.startswith("sentry_") else k: attr(v) for k, v in attrs.items()}}
    if parent:
        s["parent_span_id"] = parent
    return s


seg = "%016x" % 0x2000
items = [
    v2(0, None, "/users/1234", 0, 1.5, True, sentry_op="pageload", sentry_release="1.0.0", sentry_environment="fixture"),
    v2(1, seg, "GET https://api.example.com/v1/me?access_token=fixture-token", 0.6, 0.25, sentry_op="http.client"),
    v2(2, seg, "select * from settings where owner = 'fixture'", 0.9, 0.05, sentry_op="db.query"),
]
write("javascript-v11-stream", {"sent_at": SENT, "sdk": {"name": "sentry.javascript.browser", "version": "11.13.0"}},
      [({"type": "span", "item_count": len(items), "content_type": "application/vnd.sentry.items.span.v2+json"},
        {"version": 2, "ingest_settings": {"infer_ip": "auto", "infer_user_agent": "auto"}, "items": items})],
      "javascript", "Span v2 container (default traceLifecycle 'stream' in v11). Item shape per JS/core/src/tracing/spans/envelope.ts as summarised in the research notes; the per-span field names are INFERRED from the protocol, not captured.")

# --- Android and Cocoa app start transactions ---
a_spans = [
    span(1, ROOT, "process.load", "Process initialization", 0.0, 0.4),
    span(2, ROOT, "application.load", "ExampleApplication.onCreate", 0.4, 0.3),
    span(3, ROOT, "activity.load", "MainActivity.onCreate", 0.7, 0.5),
]
write("android-app-start", {"event_id": "b2" * 16, "sent_at": SENT, "sdk": {"name": "sentry.java.android", "version": "8.64.0"}},
      [({"type": "transaction"}, tx_event("b2" * 16, "MainActivity", "component", "ui.load", ROOT, a_spans,
                                           {"app_start_cold": {"value": 1234, "unit": "millisecond"}, "frames_total": {"value": 120, "unit": "none"}}, "java",
                                           {"contexts": {"trace": {"trace_id": TRACE, "span_id": ROOT, "op": "ui.load", "status": "ok"}, "app": {"app_name": "Fixture", "app_version": "1.0.0"}}}))],
      "java.android", "Cold app start transaction with measurements.app_start_cold = 1234 ms. Shape from the public transaction docs and sentry-java app start behaviour.")

c_spans = [
    span(1, ROOT, "app.start.cold", "Pre Runtime Init", 0.0, 0.2),
    span(2, ROOT, "app.start.cold", "UIKit Init", 0.2, 0.3),
    span(3, ROOT, "app.start.cold", "Initial Frame Render", 0.5, 0.4),
]
write("cocoa-app-start", {"event_id": "c3" * 16, "sent_at": SENT, "sdk": {"name": "sentry.cocoa", "version": "8.50.0"}},
      [({"type": "transaction"}, tx_event("c3" * 16, "ViewController", "component", "ui.load", ROOT, c_spans,
                                           {"app_start_warm": {"value": 0.62, "unit": "second"}}, "cocoa"))],
      "cocoa", "Warm app start transaction with measurements.app_start_warm = 0.62 s (the unit is converted to 620 ms).")
