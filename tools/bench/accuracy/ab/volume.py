#!/usr/bin/env python3
"""Send one generated scenario to both accuracy A/B ingest endpoints."""

from __future__ import annotations

import argparse
import concurrent.futures
import dataclasses
import json
import sys
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Callable, Mapping
from pathlib import Path
from typing import Any


@dataclasses.dataclass(frozen=True)
class Event:
    name: str
    url: str
    referrer: str
    props: dict[str, str]
    revenue: dict[str, Any] | None = None


@dataclasses.dataclass(frozen=True)
class Request:
    product: str
    url: str
    headers: dict[str, str]
    body: dict[str, Any]


@dataclasses.dataclass(frozen=True)
class Response:
    status: int
    headers: Mapping[str, str]


def _absolute_url(value: str) -> str:
    return urllib.parse.urljoin("http://app.test/", value)


def _landing_url(path: str, utm: Mapping[str, str]) -> str:
    url = _absolute_url(path)
    if not utm:
        return url
    parts = urllib.parse.urlsplit(url)
    # The scenario stores UTM keys without the prefix ("campaign"); the URL needs utm_*.
    query = urllib.parse.urlencode(
        [(key if key.startswith("utm_") else f"utm_{key}", value) for key, value in utm.items()]
    )
    return urllib.parse.urlunsplit((parts.scheme, parts.netloc, parts.path, query, parts.fragment))


def visit_events(visit: Mapping[str, Any]) -> list[Event]:
    """Translate one scenario visit to the tracker-visible event sequence."""
    current_url = "http://app.test/"
    referrer = str(visit.get("referrer", ""))
    first_navigation = True
    events: list[Event] = []
    page_path: str | None = None
    for step in visit.get("steps", []):
        kind = step.get("kind")
        if kind in {"goto", "spa", "hash"}:
            path = str(step.get("path", ""))
            # A single-page-app navigation to the page the visitor is already on is not
            # a pageview (a browser tracker would not report it either).
            repeated = kind == "spa" and path == page_path
            if kind != "hash":
                page_path = path
            if repeated:
                continue
            if kind == "hash" and path.startswith("#"):
                parts = urllib.parse.urlsplit(current_url)
                current_url = urllib.parse.urlunsplit(
                    (parts.scheme, parts.netloc, parts.path, parts.query, path[1:])
                )
            elif first_navigation:
                current_url = _landing_url(path, visit.get("utm", {}))
            else:
                current_url = _absolute_url(path)
            events.append(Event("pageview", current_url, referrer, {}))
            first_navigation = False
            continue
        if kind == "wait":
            continue
        if kind == "event":
            revenue = None
            if step.get("revenue") is not None:
                revenue = {"amount": step["revenue"], "currency": step.get("currency", "USD")}
            events.append(
                Event(
                    str(step.get("name", "")),
                    current_url,
                    referrer,
                    {str(key): str(value) for key, value in step.get("props", {}).items()},
                    revenue,
                )
            )
            continue
        if kind in {"outbound", "download"}:
            href = _absolute_url(str(step.get("href", "")))
            name = "Outbound Link: Click" if kind == "outbound" else "File Download"
            events.append(Event(name, current_url, referrer, {"url": href}))
            continue
        raise ValueError(f"unsupported scenario step kind {kind!r}")
    return events


def build_requests(
    visit: Mapping[str, Any], event: Event, stack: Mapping[str, Any]
) -> tuple[Request, Request]:
    """Build equivalent Plausible and Supermetry intake requests."""
    identity_headers = {
        "Content-Type": "application/json",
        "User-Agent": str(visit["user_agent"]),
        "X-Forwarded-For": str(visit["ip"]),
    }
    plausible_config = stack["plausible"]
    plausible_body: dict[str, Any] = {
        "name": event.name,
        "url": event.url,
        "domain": plausible_config["domain"],
        "referrer": event.referrer,
    }
    if event.props:
        plausible_body["props"] = event.props
    if event.revenue is not None:
        plausible_body["revenue"] = event.revenue
    supermetry_config = stack["supermetry"]
    supermetry_body: dict[str, Any] = {
        "site": supermetry_config["public_id"],
        "name": event.name,
        "url": event.url,
        "referrer": event.referrer,
        "screen_width": int(visit.get("viewport", [0])[0]),
        "props": event.props,
    }
    if event.revenue is not None:
        supermetry_body["revenue"] = event.revenue
    return (
        Request(
            "plausible",
            str(plausible_config["url"]).rstrip("/") + "/api/event",
            dict(identity_headers),
            plausible_body,
        ),
        Request(
            "supermetry",
            str(supermetry_config["api_url"]).rstrip("/") + "/api/public/v1/events",
            {
                **identity_headers,
                "Origin": "http://app.test",
                "Host": "app.test:18090",
            },
            supermetry_body,
        ),
    )


def http_post(request: Request) -> Response:
    data = json.dumps(request.body, separators=(",", ":")).encode()
    url_request = urllib.request.Request(
        request.url, data=data, headers=request.headers, method="POST"
    )
    try:
        with urllib.request.urlopen(url_request, timeout=10) as response:
            return Response(response.status, {key.lower(): value for key, value in response.headers.items()})
    except urllib.error.HTTPError as error:
        return Response(error.code, {key.lower(): value for key, value in error.headers.items()})


def send_visit(
    visit: Mapping[str, Any],
    stack: Mapping[str, Any],
    *,
    post: Callable[[Request], Response] = http_post,
) -> dict[str, Any]:
    """Send both copies of every event serially for one visit."""
    sent = {"plausible": 0, "supermetry": 0}
    failures: list[dict[str, Any]] = []
    for event in visit_events(visit):
        for request in build_requests(visit, event, stack):
            try:
                response = post(request)
                dropped = next(
                    (
                        value
                        for key, value in response.headers.items()
                        if key.lower() == "x-plausible-dropped"
                    ),
                    None,
                )
                if not 200 <= response.status < 300:
                    error = f"HTTP {response.status}"
                elif request.product == "plausible" and dropped is not None:
                    error = f"x-plausible-dropped: {dropped or '(empty)'}"
                else:
                    sent[request.product] += 1
                    continue
            except (OSError, TimeoutError, urllib.error.URLError) as exception:
                error = f"request failed: {type(exception).__name__}"
            failures.append(
                {
                    "visit_id": str(visit.get("id", "")),
                    "product": request.product,
                    "event": event.name,
                    "error": error,
                }
            )
    return {"visit_id": str(visit.get("id", "")), "sent": sent, "failures": failures}


def run_volume(
    scenario: Mapping[str, Any],
    stack: Mapping[str, Any],
    *,
    concurrency: int,
    report_path: Path,
    post: Callable[[Request], Response] = http_post,
    sender: Callable[..., dict[str, Any]] = send_visit,
) -> dict[str, Any]:
    if concurrency < 1:
        raise ValueError("concurrency must be at least 1")
    visits = scenario.get("visits")
    if not isinstance(visits, list):
        raise ValueError("scenario must contain a visits list")
    results: list[dict[str, Any]] = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as executor:
        futures = [executor.submit(sender, visit, stack, post=post) for visit in visits]
        for future in futures:
            results.append(future.result())
    report: dict[str, Any] = {
        "visits": len(visits),
        "sent": {
            product: sum(result["sent"][product] for result in results)
            for product in ("plausible", "supermetry")
        },
        "failures": [failure for result in results for failure in result["failures"]],
    }
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    return report


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--scenario", type=Path, required=True)
    parser.add_argument("--stack", type=Path, required=True)
    parser.add_argument("--concurrency", type=int, default=16)
    parser.add_argument("--report", type=Path, default=Path("volume-report.json"))
    args = parser.parse_args(argv)
    try:
        scenario = json.loads(args.scenario.read_text(encoding="utf-8"))
        stack = json.loads(args.stack.read_text(encoding="utf-8"))
        report = run_volume(
            scenario,
            stack,
            concurrency=args.concurrency,
            report_path=args.report,
        )
    except (OSError, ValueError, KeyError, json.JSONDecodeError) as error:
        print(f"volume run failed: {error}", file=sys.stderr)
        return 1
    print(json.dumps(report, sort_keys=True))
    return 1 if report["failures"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
