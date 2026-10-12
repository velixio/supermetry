#!/usr/bin/env python3
"""Compare Supermetry and Plausible Stats API results for one real site."""

from __future__ import annotations

import argparse
import http.cookiejar
import json
import os
from pathlib import Path
import re
import statistics
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from typing import Any


SUMMARY_METRICS = ["visitors", "visits", "pageviews", "events", "bounce_rate"]
DEFAULT_CREDS = "/tmp/claude-1000/-home-user-code-supermetry/a367a7b0-447b-4879-9004-f5690263327e/scratchpad/cmp-creds.env"


class BrowserSession:
    """Small cookie-aware JSON client used only for read-only observation."""

    def __init__(self) -> None:
        self.cookies = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.cookies))
        self.csrf_token = ""

    def request(self, url: str, *, method: str = "GET", body: Any = None, headers: dict[str, str] | None = None) -> Any:
        request_headers = {"Accept": "application/json", "User-Agent": "Mozilla/5.0 accuracy-live read-only observer"}
        request_headers.update(headers or {})
        data = None
        if body is not None:
            data = json.dumps(body, separators=(",", ":")).encode()
            request_headers["Content-Type"] = "application/json"
        request = urllib.request.Request(url, data=data, method=method, headers=request_headers)
        try:
            with self.opener.open(request, timeout=30) as response:
                raw = response.read()
                content_type = response.headers.get("Content-Type", "")
                if "json" in content_type:
                    return json.loads(raw or b"null")
                return raw.decode(errors="replace"), response.geturl()
        except urllib.error.HTTPError as error:
            # Never include response bodies: authentication failures sometimes
            # echo submitted fields, and credentials must not reach output.
            raise RuntimeError(f"{urllib.parse.urlsplit(url).path}: HTTP {error.code}") from None

    def csrf(self) -> str:
        for cookie in self.cookies:
            if "csrf" in cookie.name.lower():
                return urllib.parse.unquote(cookie.value)
        return ""


def load_credentials(path: str | Path) -> dict[str, str]:
    """Parse KEY=VALUE credentials without expanding, logging, or copying them."""
    result: dict[str, str] = {}
    for line in Path(path).read_text(encoding="utf-8").splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#") or "=" not in stripped:
            continue
        key, value = stripped.split("=", 1)
        result[key.strip()] = value.strip()
    required = {f"{product}_{field}" for product in ("PLAUSIBLE", "UMAMI", "SUPERMETRY_LIVE") for field in ("URL", "USER", "PASS")}
    missing = sorted(required - result.keys())
    if missing:
        raise ValueError("credential file is missing keys: " + ", ".join(missing))
    return result


def _url(base: str, path: str) -> str:
    return base.rstrip("/") + "/" + path.lstrip("/")


def _first(value: Any) -> dict[str, Any]:
    if isinstance(value, list):
        return value[0] if value else {}
    if isinstance(value, dict):
        rows = value.get("data", value.get("websites", []))
        if isinstance(rows, list):
            return rows[0] if rows else {}
    return {}


def login_supermetry(creds: dict[str, str]) -> tuple[BrowserSession, int]:
    session = BrowserSession()
    base = creds["SUPERMETRY_LIVE_URL"]
    session.request(_url(base, "/api/private/auth/config"))
    origin = f"{urllib.parse.urlsplit(base).scheme}://{urllib.parse.urlsplit(base).netloc}"
    session.request(_url(base, "/api/private/auth/login"), method="POST", body={"email": creds["SUPERMETRY_LIVE_USER"], "password": creds["SUPERMETRY_LIVE_PASS"]}, headers={"Origin": origin, "Referer": base.rstrip("/") + "/login"})
    current = session.request(_url(base, "/api/private/me"))
    sites = [site for membership in current.get("memberships", []) for site in membership.get("sites", [])]
    site = _first(sites)
    if not site:
        raise RuntimeError("Supermetry account has no visible site")
    return session, int(site["id"])


def login_umami(creds: dict[str, str]) -> tuple[BrowserSession, str, str]:
    session = BrowserSession()
    base = creds["UMAMI_URL"]
    auth = session.request(_url(base, "/api/auth/login"), method="POST", body={"username": creds["UMAMI_USER"], "password": creds["UMAMI_PASS"]})
    token = str(auth.get("token", ""))
    if not token:
        raise RuntimeError("Umami login returned no token")
    headers = {"Authorization": f"Bearer {token}"}
    site = _first(session.request(_url(base, "/api/websites"), headers=headers))
    if not site:
        raise RuntimeError("Umami account has no visible website")
    return session, token, str(site["id"])


def login_plausible(creds: dict[str, str]) -> tuple[BrowserSession, str]:
    session = BrowserSession()
    base = creds["PLAUSIBLE_URL"]
    page, _ = session.request(_url(base, "/login"), headers={"Accept": "text/html"})
    token_match = re.search(r'(?:name="_csrf_token"[^>]*value|name="csrf-token" content)="([^"]+)"', page)
    fields = {"email": creds["PLAUSIBLE_USER"], "password": creds["PLAUSIBLE_PASS"], "return_to": "/"}
    if token_match:
        fields["_csrf_token"] = token_match.group(1)
    request = urllib.request.Request(
        _url(base, "/login"),
        data=urllib.parse.urlencode(fields).encode(),
        method="POST",
        headers={"Accept": "text/html", "Content-Type": "application/x-www-form-urlencoded", "User-Agent": "Mozilla/5.0 accuracy-live read-only observer", "Origin": f"{urllib.parse.urlsplit(base).scheme}://{urllib.parse.urlsplit(base).netloc}"},
    )
    with session.opener.open(request, timeout=30) as response:
        html = response.read().decode(errors="replace")
        final_path = urllib.parse.unquote(urllib.parse.urlsplit(response.geturl()).path).strip("/")
    csrf_match = re.search(r'<meta[^>]+name="csrf-token"[^>]+content="([^"]+)"', html)
    if csrf_match:
        session.csrf_token = csrf_match.group(1)
    configured = os.environ.get("PLAUSIBLE_SITE", "")
    if configured:
        return session, configured
    candidates = re.findall(r'href="/([^"?#/]+)(?:/[^"?#]*)?"', html)
    reserved = {"login", "register", "sites", "settings", "billing", "account", "logout"}
    site = final_path.split("/", 1)[0] if final_path and final_path not in reserved else next((item for item in candidates if item not in reserved and "." in item), "")
    if not site:
        raise RuntimeError("could not discover Plausible site; set PLAUSIBLE_SITE")
    return session, site


def summarize_live(samples: list[dict[str, Any]]) -> dict[str, Any]:
    """Quantify pairwise deltas and sampling skew without speculative causes."""
    products = ("supermetry", "plausible", "umami")
    result: dict[str, Any] = {"samples": len(samples), "pairs": {}}
    for index, left in enumerate(products):
        values = [float(row[left]["online"]) for row in samples]
        result[left] = {"mean_online": statistics.fmean(values), "min_online": min(values), "max_online": max(values)}
        for right in products[index + 1:]:
            deltas = [float(row[left]["online"]) - float(row[right]["online"]) for row in samples]
            relative = [delta / max(float(row[right]["online"]), 1.0) * 100 for delta, row in zip(deltas, samples)]
            result["pairs"][f"{left}_vs_{right}"] = {"mean_delta": statistics.fmean(deltas), "mean_percent": statistics.fmean(relative)}
    skews = [float(row.get("sampling_skew_ms", 0)) for row in samples]
    result["sampling_skew_ms"] = {"mean": statistics.fmean(skews), "max": max(skews)}
    return result


def _safe_context(fetch: Any) -> dict[str, Any]:
    try:
        value = fetch()
        return value if isinstance(value, dict) else {"data": value}
    except (OSError, RuntimeError, urllib.error.URLError, ValueError):
        return {"unavailable": True}


def fetch_observation_context(
    creds: dict[str, str],
    supermetry: BrowserSession,
    supermetry_site: int,
    plausible: BrowserSession,
    plausible_site: str,
    umami: BrowserSession,
    umami_site: str,
    umami_headers: dict[str, str],
) -> dict[str, Any]:
    """Fetch aligned totals and breakdowns once, without changing remote state."""
    now = datetime.now(timezone.utc)
    start_ms, end_ms = int((now.timestamp() - 86400) * 1000), int(now.timestamp() * 1000)
    today = now.date().isoformat()
    supermetry_headers = {"X-CSRF-Token": supermetry.csrf()}

    def supermetry_query(dimensions: list[str], metrics: list[str], limit: int = 1000) -> dict[str, Any]:
        return supermetry.request(
            _url(creds["SUPERMETRY_LIVE_URL"], "/api/private/query"),
            method="POST",
            headers=supermetry_headers,
            body={"site_id": supermetry_site, "date_range": {"from": today, "to": today}, "dimensions": dimensions, "metrics": metrics, "limit": limit},
        )

    def plausible_query(dimensions: list[str], metrics: list[str], limit: int = 1000) -> dict[str, Any]:
        return plausible.request(
            _url(creds["PLAUSIBLE_URL"], f"/api/stats/{urllib.parse.quote(plausible_site, safe='')}/query"),
            method="POST",
            headers={"X-CSRF-Token": plausible.csrf_token},
            body={"date_range": "24h", "dimensions": dimensions, "metrics": metrics, "pagination": {"limit": limit, "offset": 0}, "include": {"time_labels": True}},
        )

    umami_params = urllib.parse.urlencode({"startAt": start_ms, "endAt": end_ms, "unit": "minute", "timezone": "UTC"})
    return {
        "captured_at": now.isoformat(),
        "supermetry": {
            "totals": _safe_context(lambda: supermetry_query([], ["visitors", "visits", "pageviews"])),
            "minute_series": _safe_context(lambda: supermetry_query(["time"], ["visitors", "visits", "pageviews"])),
            "countries": _safe_context(lambda: supermetry_query(["country"], ["visitors"], 10)),
            "browsers": _safe_context(lambda: supermetry_query(["browser"], ["visitors"], 10)),
            "note": "This API exposes site-local today rather than a rolling 24-hour shorthand.",
        },
        "plausible": {
            "totals": _safe_context(lambda: plausible_query([], ["visitors", "visits", "pageviews"])),
            "minute_series": _safe_context(lambda: plausible_query(["time:minute"], ["visitors", "visits", "pageviews"])),
            "countries": _safe_context(lambda: plausible_query(["visit:country_name"], ["visitors"], 10)),
            "browsers": _safe_context(lambda: plausible_query(["visit:browser"], ["visitors"], 10)),
        },
        "umami": {
            "totals": _safe_context(lambda: umami.request(_url(creds["UMAMI_URL"], f"/api/websites/{urllib.parse.quote(umami_site, safe='')}/stats?{umami_params}"), headers=umami_headers)),
            "minute_series": _safe_context(lambda: umami.request(_url(creds["UMAMI_URL"], f"/api/websites/{urllib.parse.quote(umami_site, safe='')}/pageviews?{umami_params}"), headers=umami_headers)),
            "countries": _safe_context(lambda: umami.request(_url(creds["UMAMI_URL"], f"/api/websites/{urllib.parse.quote(umami_site, safe='')}/metrics?{umami_params}&type=country&limit=10"), headers=umami_headers)),
            "browsers": _safe_context(lambda: umami.request(_url(creds["UMAMI_URL"], f"/api/websites/{urllib.parse.quote(umami_site, safe='')}/metrics?{umami_params}&type=browser&limit=10"), headers=umami_headers)),
        },
    }


def request_json(url: str, key: str, body: dict[str, Any]) -> dict[str, Any]:
    request = urllib.request.Request(
        url,
        data=json.dumps(body, separators=(",", ":")).encode(),
        method="POST",
        headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        detail = error.read(500).decode(errors="replace")
        raise RuntimeError(f"{url}: HTTP {error.code}: {detail}") from None


def fetch_panel(base: str, key: str, site_id: int, date_from: str, date_to: str) -> dict[str, Any]:
    endpoint = base.rstrip("/") + "/api/public/v1/query"

    def query(dimensions: list[str], metrics: list[str], limit: int = 1000) -> dict[str, Any]:
        return request_json(
            endpoint,
            key,
            {
                "site_id": site_id,
                "date_range": {"from": date_from, "to": date_to},
                "dimensions": dimensions,
                "metrics": metrics,
                "limit": limit,
            },
        )

    summary = query([], SUMMARY_METRICS)
    pages = query(["page"], ["pageviews"], 10)
    sources = query(["source"], ["visitors"], 10)
    return {
        "totals": summary["totals"],
        "pages": [row["page"] for row in pages.get("rows", [])[:10]],
        "sources": [_normal_source(row["source"]) for row in sources.get("rows", [])[:10]],
    }


def fetch_plausible(base: str, key: str, site: str, date_from: str, date_to: str) -> dict[str, Any]:
    endpoint = base.rstrip("/") + "/api/v2/query"

    def query(dimensions: list[str], metrics: list[str], limit: int = 1000) -> dict[str, Any]:
        return request_json(
            endpoint,
            key,
            {
                "site_id": site,
                "date_range": [date_from, date_to],
                "dimensions": dimensions,
                "metrics": metrics,
                "order_by": [[metrics[0], "desc"]],
                "pagination": {"limit": limit, "offset": 0},
            },
        )

    summary_response = query([], SUMMARY_METRICS)
    summary_values = summary_response.get("results", [{}])[0].get("metrics", [])
    totals = dict(zip(SUMMARY_METRICS, summary_values))
    pages = query(["event:page"], ["pageviews"], 10).get("results", [])
    sources = query(["visit:source"], ["visitors"], 10).get("results", [])
    return {
        "totals": totals,
        "pages": [row["dimensions"][0] for row in pages[:10]],
        "sources": [_normal_source(row["dimensions"][0]) for row in sources[:10]],
    }


def compare_snapshots(supermetry: dict[str, Any], plausible: dict[str, Any]) -> list[dict[str, Any]]:
    rows = []
    for metric in SUMMARY_METRICS:
        left = float(supermetry["totals"].get(metric, 0))
        right = float(plausible["totals"].get(metric, 0))
        if metric in {"pageviews", "events"}:
            passed = left == right
            tolerance = "exact"
        elif metric == "bounce_rate":
            passed = abs(left - right) <= 2
            tolerance = "2 pp"
        else:
            denominator = max(abs(right), 1)
            passed = abs(left - right) / denominator <= 0.02
            tolerance = "2%"
        rows.append(_row(metric, left, right, tolerance, passed))
    for metric, key in (("top_pages", "pages"), ("top_sources", "sources")):
        left = set(supermetry[key][:10])
        right = set(plausible[key][:10])
        rows.append(_row(metric, sorted(left), sorted(right), "same set", left == right))
    return rows


def render_delta_table(rows: list[dict[str, Any]]) -> str:
    output = ["| Metric | Supermetry | Plausible | Delta | Tolerance | Result |", "|---|---:|---:|---:|---|---|"]
    for row in rows:
        output.append(
            f"| {row['metric']} | {_display(row['supermetry'])} | {_display(row['plausible'])} | "
            f"{_display(row['delta'])} | {row['tolerance']} | {'PASS' if row['pass'] else 'FAIL'} |"
        )
    return "\n".join(output)


def _row(metric: str, supermetry: Any, plausible: Any, tolerance: str, passed: bool) -> dict[str, Any]:
    delta = supermetry - plausible if isinstance(supermetry, float) else "set"
    return {"metric": metric, "supermetry": supermetry, "plausible": plausible, "delta": delta, "tolerance": tolerance, "pass": passed}


def _display(value: Any) -> str:
    if isinstance(value, float):
        return f"{value:.3f}"
    if isinstance(value, list):
        return ", ".join(value)
    return str(value)


def _normal_source(value: str) -> str:
    return "(direct)" if value in {"Direct / None", "Direct", "(direct)"} else value


def observe_live(creds: dict[str, str], *, samples: int = 31, interval: float = 30.0) -> tuple[list[dict[str, Any]], dict[str, Any], dict[str, Any]]:
    """Authenticate normally and sample only analytics read endpoints."""
    supermetry, supermetry_site = login_supermetry(creds)
    umami, umami_token, umami_site = login_umami(creds)
    plausible, plausible_site = login_plausible(creds)
    umami_headers = {"Authorization": f"Bearer {umami_token}"}
    rows: list[dict[str, Any]] = []
    for index in range(samples):
        began = time.monotonic()
        supermetry_value = supermetry.request(_url(creds["SUPERMETRY_LIVE_URL"], f"/api/private/realtime?site_id={supermetry_site}"))
        plausible_value = plausible.request(_url(creds["PLAUSIBLE_URL"], f"/api/stats/{urllib.parse.quote(plausible_site, safe='')}/current-visitors"))
        umami_value = umami.request(_url(creds["UMAMI_URL"], f"/api/websites/{urllib.parse.quote(umami_site, safe='')}/active"), headers=umami_headers)
        ended = time.monotonic()
        rows.append({
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "sampling_skew_ms": round((ended - began) * 1000, 3),
            "supermetry": {
                "online": int(supermetry_value.get("active_visitors", 0)),
                "visitors_last_30m": int(supermetry_value.get("visitors_last_30m", 0)),
                "pageviews_last_30m": int(supermetry_value.get("pageviews_last_30m", 0)),
                "visitors_per_minute": supermetry_value.get("visitors_per_minute", []),
                "countries": supermetry_value.get("countries", []),
                "browsers": supermetry_value.get("browsers", []),
            },
            "plausible": {"online": int(plausible_value)},
            "umami": {"online": int((umami_value or {}).get("visitors", 0))},
        })
        if index + 1 < samples:
            elapsed = time.monotonic() - began
            time.sleep(max(interval - elapsed, 0))
    context = fetch_observation_context(
        creds, supermetry, supermetry_site, plausible, plausible_site,
        umami, umami_site, umami_headers,
    )
    return rows, summarize_live(rows), context


def render_live_summary(summary: dict[str, Any]) -> str:
    lines = [
        f"Live samples: {summary['samples']}",
        f"Sampling skew: mean {summary['sampling_skew_ms']['mean']:.0f} ms, max {summary['sampling_skew_ms']['max']:.0f} ms",
        "| Pair | Mean delta | Mean delta % |",
        "|---|---:|---:|",
    ]
    for pair, values in summary["pairs"].items():
        lines.append(f"| {pair.replace('_vs_', ' vs ')} | {values['mean_delta']:.2f} | {values['mean_percent']:.2f}% |")
    return "\n".join(lines)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--from", dest="date_from")
    parser.add_argument("--to", dest="date_to")
    parser.add_argument("--live", action="store_true", help="sample the three signed-in dashboards read-only")
    parser.add_argument("--creds", default=os.environ.get("ACCURACY_CREDS", DEFAULT_CREDS))
    parser.add_argument("--samples", type=int, default=31, help="31 samples span 15 minutes at the default interval")
    parser.add_argument("--interval", type=float, default=30.0)
    parser.add_argument("--out", type=Path)
    args = parser.parse_args(argv)
    if args.live:
        if args.samples <= 0 or args.interval < 0:
            parser.error("--samples must be positive and --interval must be non-negative")
        try:
            samples, summary, context = observe_live(load_credentials(args.creds), samples=args.samples, interval=args.interval)
        except (OSError, ValueError, RuntimeError, urllib.error.URLError) as error:
            print(f"accuracy-live: {error}", file=sys.stderr)
            return 2
        if args.out:
            args.out.parent.mkdir(parents=True, exist_ok=True)
            args.out.write_text(json.dumps({"samples": samples, "summary": summary, "context": context}, indent=2) + "\n", encoding="utf-8")
        print(render_live_summary(summary))
        return 0
    if not args.date_from or not args.date_to:
        parser.error("--from and --to are required unless --live is used")
    required = ["SUPERMETRY_URL", "SUPERMETRY_KEY", "SUPERMETRY_SITE_ID", "PLAUSIBLE_URL", "PLAUSIBLE_KEY", "PLAUSIBLE_SITE"]
    missing = [name for name in required if not os.environ.get(name)]
    if missing:
        parser.error("missing environment variables: " + ", ".join(missing))
    supermetry = fetch_panel(
        os.environ["SUPERMETRY_URL"], os.environ["SUPERMETRY_KEY"], int(os.environ["SUPERMETRY_SITE_ID"]), args.date_from, args.date_to
    )
    plausible = fetch_plausible(
        os.environ["PLAUSIBLE_URL"], os.environ["PLAUSIBLE_KEY"], os.environ["PLAUSIBLE_SITE"], args.date_from, args.date_to
    )
    rows = compare_snapshots(supermetry, plausible)
    print(render_delta_table(rows))
    return 0 if all(row["pass"] for row in rows) else 1


if __name__ == "__main__":
    sys.exit(main())
