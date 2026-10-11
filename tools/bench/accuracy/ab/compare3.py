#!/usr/bin/env python3
"""Three-way accuracy comparison for truth, Supermetry, and Plausible."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from compare_live import SUMMARY_METRICS, _normal_source, request_json


METRICS = [
    "visitors",
    "visits",
    "pageviews",
    "events",
    "bounce_rate",
    "top_pages",
    "top_sources",
    "utm_campaign",
    "devices",
    "browsers",
    "oses",
    "custom_events",
    "revenue",
]
BROKEN_MESSAGE = "tracker did not fire: check navigator.webdriver, host name, Do-Not-Track, UA"
_TOP_METRICS = {"top_pages", "top_sources"}
_MISSING = object()


def fetch_supermetry(stack: dict[str, Any], date_from: str, date_to: str) -> dict[str, Any]:
    """Fetch Supermetry statistics in the truth-model shape."""
    config = stack["supermetry"]
    endpoint = (config.get("api_url") or config["url"]).rstrip("/") + "/api/public/v1/query"

    def query(dimensions: list[str], metrics: list[str], limit: int = 1000) -> dict[str, Any]:
        body: dict[str, Any] = {
            "site_id": config["site_id"],
            "date_range": {"from": date_from, "to": date_to},
            "dimensions": dimensions,
            "metrics": metrics,
            "limit": limit,
        }
        if dimensions:
            body["order_by"] = [{"column": metrics[0], "direction": "desc"}]
        return request_json(endpoint, config["key"], body)

    summary = query([], SUMMARY_METRICS).get("totals", {})
    pages = query(["page"], ["pageviews"], 10).get("rows", [])
    sources = query(["source"], ["visitors"], 10).get("rows", [])
    campaigns = query(["utm_campaign"], ["visitors"]).get("rows", [])
    devices = query(["device"], ["visitors"]).get("rows", [])
    browsers = query(["browser"], ["visitors"]).get("rows", [])
    operating_systems = query(["os"], ["visitors"]).get("rows", [])
    events_response = query(["event"], ["events", "total_revenue"])
    event_rows = events_response.get("rows", [])

    result = {name: summary.get(name) for name in SUMMARY_METRICS}
    result.update(
        {
            "top_pages": _pairs(pages, "page", "pageviews"),
            "top_sources": [
                [_normalise_source(str(row.get("source", ""))), row.get("visitors", 0)]
                for row in sources
            ],
            "utm_campaign": _mapping(campaigns, "utm_campaign", "visitors"),
            "devices": _mapping(devices, "device", "visitors", str.lower),
            "browsers": _mapping(browsers, "browser", "visitors"),
            "oses": _mapping(operating_systems, "os", "visitors"),
            "custom_events": _mapping(
                [row for row in event_rows if row.get("event") not in {None, "", "pageview"}],
                "event",
                "events",
            ),
            "revenue": _revenue_from_supermetry(events_response),
        }
    )
    return result


def fetch_plausible(stack: dict[str, Any], date_from: str, date_to: str) -> dict[str, Any]:
    """Fetch Plausible statistics in the truth-model shape."""
    config = stack["plausible"]
    endpoint = config["url"].rstrip("/") + "/api/v2/query"

    def query(dimensions: list[str], metrics: list[str], limit: int = 1000) -> dict[str, Any]:
        body: dict[str, Any] = {
            "site_id": config["domain"],
            "date_range": [date_from, date_to],
            "dimensions": dimensions,
            "metrics": metrics,
            "pagination": {"limit": limit, "offset": 0},
        }
        if dimensions:
            body["order_by"] = [[metrics[0], "desc"]]
        return request_json(endpoint, config["key"], body)

    summary_response = query([], SUMMARY_METRICS)
    summary_values = summary_response.get("results", [{}])[0].get("metrics", [])
    summary = dict(zip(SUMMARY_METRICS, summary_values))
    pages = query(["event:page"], ["pageviews"], 10).get("results", [])
    sources = query(["visit:source"], ["visitors"], 10).get("results", [])
    campaigns = query(["visit:utm_campaign"], ["visitors"]).get("results", [])
    devices = query(["visit:device"], ["visitors"]).get("results", [])
    browsers = query(["visit:browser"], ["visitors"]).get("results", [])
    operating_systems = query(["visit:os"], ["visitors"]).get("results", [])
    # event:name exposes all custom events and does not require registered goals.
    try:
        events = query(["event:name"], ["events", "total_revenue"]).get("results", [])
        revenue_supported = True
    except RuntimeError as error:
        # Plausible Community Edition does not have revenue reporting: its v2 API
        # answers HTTP 400 'Invalid metric "total_revenue"'. Anything else is a real error.
        if "total_revenue" not in str(error):
            raise
        events = query(["event:name"], ["events"]).get("results", [])
        revenue_supported = False

    result = {name: summary.get(name) for name in SUMMARY_METRICS}
    result.update(
        {
            "top_pages": _plausible_pairs(pages, normalise=False),
            "top_sources": _plausible_pairs(sources, normalise=True),
            "utm_campaign": _plausible_mapping(campaigns),
            "devices": _plausible_mapping(devices, str.lower),
            "browsers": _plausible_mapping(browsers),
            "oses": _plausible_mapping(operating_systems),
            "custom_events": _plausible_mapping(
                [row for row in events if _plausible_dimension(row) not in {"", "pageview"}]
            ),
            "revenue": _revenue_from_plausible(events) if revenue_supported else None,
        }
    )
    return result


def fetch_pageviews(stack: dict[str, Any], date_from: str, date_to: str) -> tuple[Any, Any]:
    """Cheap probe for the stability loop: one query per product, pageviews only."""
    supermetry = stack["supermetry"]
    plausible = stack["plausible"]
    sm = request_json(
        supermetry.get("api_url", supermetry["url"]).rstrip("/") + "/api/public/v1/query",
        supermetry["key"],
        {"site_id": supermetry["site_id"], "date_range": {"from": date_from, "to": date_to}, "metrics": ["pageviews"]},
    )
    pl = request_json(
        plausible["url"].rstrip("/") + "/api/v2/query",
        plausible["key"],
        {"site_id": plausible["domain"], "date_range": [date_from, date_to], "metrics": ["pageviews"]},
    )
    return (
        sm.get("totals", {}).get("pageviews"),
        (pl.get("results") or [{}])[0].get("metrics", [None])[0],
    )


def compare(
    truth: dict[str, Any],
    supermetry: dict[str, Any],
    plausible: dict[str, Any],
    known: list[dict[str, str]],
) -> list[dict[str, Any]]:
    """Compare both products with truth and classify every metric."""
    broken = {
        product
        for product, values in (("supermetry", supermetry), ("plausible", plausible))
        if _tracker_broken(truth, values)
    }
    rows = []
    for metric in METRICS:
        truth_value = _truth_value(truth, metric)
        actuals = {"supermetry": supermetry.get(metric, _MISSING), "plausible": plausible.get(metric, _MISSING)}
        verdicts: dict[str, str] = {}
        mismatches = []
        explained = []
        for product, actual in actuals.items():
            if product in broken:
                verdicts[product] = BROKEN_MESSAGE
                continue
            expected = _expected_for_product(truth_value, metric, product)
            if actual is _MISSING or actual is None:
                # A product that cannot report a metric at all is a documented
                # difference only when known_differences.json says so.
                entry = _known_difference(known, metric, product)
                if entry is not None:
                    verdicts[product] = entry["reason"]
                    explained.append(product)
                else:
                    verdicts[product] = "not available"
                    mismatches.append(product)
                continue
            if _matches(metric, expected, actual):
                verdicts[product] = "MATCH"
                continue
            entry = _known_difference(known, metric, product)
            if entry is not None:
                verdicts[product] = entry["reason"]
                explained.append(product)
            else:
                verdicts[product] = "mismatch"
                mismatches.append(product)

        if broken:
            status = "BROKEN"
        elif mismatches:
            status = "UNEXPLAINED"
        elif explained:
            status = "EXPLAINED"
        else:
            status = "MATCH"
        rows.append(
            {
                "metric": metric,
                "truth": truth_value,
                "supermetry": None if actuals["supermetry"] is _MISSING else actuals["supermetry"],
                "plausible": None if actuals["plausible"] is _MISSING else actuals["plausible"],
                "verdict_supermetry": verdicts["supermetry"],
                "verdict_plausible": verdicts["plausible"],
                "status": status,
            }
        )
    return rows


def load_known_differences(path: Path | None = None) -> list[dict[str, str]]:
    source = path or Path(__file__).with_name("known_differences.json")
    data = json.loads(source.read_text(encoding="utf-8"))
    if not isinstance(data, list):
        raise ValueError("known differences must be a JSON array")
    return data


def render_markdown(rows: list[dict[str, Any]], meta: dict[str, Any]) -> str:
    lines = [
        "# Accuracy A/B report",
        "",
        f"- Date range: {meta['date_from']} to {meta['date_to']} (UTC)",
        f"- Ingestion: {meta['stability']}",
        "",
        "| Metric | Truth | Supermetry | Plausible | Supermetry verdict | Plausible verdict | Status |",
        "|---|---:|---:|---:|---|---|---|",
    ]
    for row in rows:
        lines.append(
            "| " + " | ".join(
                _markdown(value)
                for value in (
                    row["metric"],
                    row.get("truth"),
                    row.get("supermetry"),
                    row.get("plausible"),
                    row.get("verdict_supermetry", ""),
                    row.get("verdict_plausible", ""),
                    row["status"],
                )
            ) + " |"
        )
    return "\n".join(lines) + "\n"


def render_json(rows: list[dict[str, Any]], meta: dict[str, Any]) -> str:
    return json.dumps({"meta": meta, "rows": rows}, indent=2, sort_keys=True) + "\n"


def _truth_value(truth: dict[str, Any], metric: str) -> Any:
    if metric != "bounce_rate":
        return truth.get(metric)
    if "bounce_rate" in truth:
        return truth["bounce_rate"]
    return {
        "supermetry": truth.get("bounce_rate_supermetry"),
        "plausible": truth.get("bounce_rate_plausible"),
    }


def _expected_for_product(truth_value: Any, metric: str, product: str) -> Any:
    if metric == "bounce_rate" and isinstance(truth_value, dict):
        return truth_value.get(product)
    return truth_value


def _matches(metric: str, expected: Any, actual: Any) -> bool:
    if expected is None:
        return False
    if metric in {"visitors", "visits"}:
        return abs(float(actual) - float(expected)) / max(abs(float(expected)), 1.0) <= 0.02
    if metric == "bounce_rate":
        return abs(float(actual) - float(expected)) <= 2.0
    if metric in _TOP_METRICS:
        return {_frozen(item) for item in expected} == {_frozen(item) for item in actual}
    return actual == expected


def _tracker_broken(truth: dict[str, Any], actual: dict[str, Any]) -> bool:
    expected = truth.get("pageviews")
    observed = actual.get("pageviews")
    return isinstance(expected, (int, float)) and expected > 0 and isinstance(observed, (int, float)) and observed < expected * 0.5


def _known_difference(known: list[dict[str, str]], metric: str, product: str) -> dict[str, str] | None:
    return next((item for item in known if item.get("metric") == metric and item.get("product") == product), None)


def _frozen(value: Any) -> Any:
    if isinstance(value, list):
        return tuple(_frozen(item) for item in value)
    if isinstance(value, dict):
        return tuple(sorted((key, _frozen(item)) for key, item in value.items()))
    return value


def _pairs(rows: list[dict[str, Any]], dimension: str, metric: str) -> list[list[Any]]:
    return [[row.get(dimension, ""), row.get(metric, 0)] for row in rows]


def _mapping(
    rows: list[dict[str, Any]],
    dimension: str,
    metric: str,
    normalise=lambda value: value,
) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for row in rows:
        key = normalise(str(row.get(dimension, "")))
        if key:
            result[key] = result.get(key, 0) + row.get(metric, 0)
    return result


def _plausible_dimension(row: dict[str, Any]) -> str:
    dimensions = row.get("dimensions", [])
    return str(dimensions[0]) if dimensions else ""


def _plausible_pairs(rows: list[dict[str, Any]], normalise: bool) -> list[list[Any]]:
    result = []
    for row in rows:
        value = _plausible_dimension(row)
        if normalise:
            value = _normalise_source(value)
        metrics = row.get("metrics", [])
        result.append([value, metrics[0] if metrics else 0])
    return result


def _plausible_mapping(rows: list[dict[str, Any]], normalise=lambda value: value) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for row in rows:
        key = normalise(_plausible_dimension(row))
        metrics = row.get("metrics", [])
        if key and metrics:
            result[key] = result.get(key, 0) + metrics[0]
    return result


def _normalise_source(value: str) -> str:
    normalised = _normal_source(value)
    return "Twitter" if normalised == "X/Twitter" else normalised


def _revenue_from_supermetry(response: dict[str, Any]) -> dict[str, Any]:
    total = response.get("totals", {}).get("total_revenue")
    return {} if total in {None, 0} else {"USD": total}


def _revenue_from_plausible(rows: list[dict[str, Any]]) -> dict[str, Any]:
    totals: dict[str, Any] = {}
    for row in rows:
        metrics = row.get("metrics", [])
        if len(metrics) < 2:
            continue
        revenue = metrics[1]
        if isinstance(revenue, dict):
            value = revenue.get("value", 0)
            currency = revenue.get("currency", "USD")
        else:
            value = revenue
            currency = "USD"
        if value:
            totals[currency] = totals.get(currency, 0) + value
    return totals


def _markdown(value: Any) -> str:
    if value is None:
        text = "not available"
    elif isinstance(value, (dict, list, tuple)):
        text = json.dumps(value, sort_keys=True, separators=(",", ":"))
    else:
        text = str(value)
    return text.replace("|", "\\|").replace("\n", " ")
