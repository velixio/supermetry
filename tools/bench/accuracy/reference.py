#!/usr/bin/env python3
"""Independent metric reference for Supermetry benchmark NDJSON.

This module intentionally uses only Python's standard library and the public
NDJSON/API contracts. It does not import product code.
"""

from __future__ import annotations

import json
from collections import defaultdict
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Any, Iterable
from urllib.parse import urlsplit
from zoneinfo import ZoneInfo


DEFAULT_METRICS = (
    "visitors",
    "visits",
    "pageviews",
    "views_per_visit",
    "bounce_rate",
    "visit_duration",
)

SESSION_DIMENSIONS = {
    "source",
    "utm_source",
    "utm_medium",
    "utm_campaign",
    "country",
    "device",
    "browser",
    "os",
    "entry",
    "exit",
}

SOURCE_RULES = (
    ("Google", "organic", ("google.*",), ("google",)),
    ("Bing", "organic", ("bing.com",), ("bing",)),
    ("DuckDuckGo", "organic", ("duckduckgo.com",), ("duckduckgo",)),
    ("Yahoo", "organic", ("yahoo.com",), ("yahoo",)),
    ("Yandex", "organic", ("yandex.com", "yandex.ru"), ("yandex",)),
    ("Baidu", "organic", ("baidu.com",), ("baidu",)),
    ("Ecosia", "organic", ("ecosia.org",), ("ecosia",)),
    ("Facebook", "social", ("facebook.com", "l.facebook.com", "m.facebook.com", "lm.facebook.com"), ("facebook", "fb")),
    ("Instagram", "social", ("instagram.com",), ("instagram",)),
    ("X/Twitter", "social", ("t.co", "twitter.com", "x.com"), ("twitter", "x")),
    ("LinkedIn", "social", ("lnkd.in", "linkedin.com"), ("linkedin",)),
    ("Reddit", "social", ("reddit.com",), ("reddit",)),
    ("YouTube", "social", ("youtube.com", "youtu.be"), ("youtube",)),
    ("Pinterest", "social", ("pinterest.com", "pin.it"), ("pinterest",)),
    ("GitHub", "referral", ("github.com",), ("github",)),
    ("Hacker News", "referral", ("news.ycombinator.com",), ("hacker news", "hackernews", "hn")),
    ("ChatGPT", "referral", ("chatgpt.com", "chat.openai.com"), ("chatgpt",)),
    ("Perplexity", "referral", ("perplexity.ai",), ("perplexity",)),
)

MEDIUM_ALIASES = {
    "organic": "organic",
    "search": "organic",
    "social": "social",
    "social-network": "social",
    "social_media": "social",
    "social-media": "social",
    "referral": "referral",
    "email": "email",
    "e-mail": "email",
    "paid": "paid",
    "cpc": "paid",
    "ppc": "paid",
    "paid-search": "paid",
    "paid_search": "paid",
    "paid-social": "paid",
    "paid_social": "paid",
}


@dataclass
class Event:
    ts: datetime
    name: str
    path: str
    props: dict[str, str]
    raw: dict[str, Any]


@dataclass
class Session:
    site: int
    day: str
    visitor: str
    key: str
    source: str
    utm_source: str
    utm_medium: str
    utm_campaign: str
    country: str
    device: str
    browser: str
    os: str
    events: list[Event] = field(default_factory=list)

    @property
    def pages(self) -> list[Event]:
        return [event for event in self.events if event.name == "pageview"]

    @property
    def customs(self) -> list[Event]:
        return [event for event in self.events if event.name != "pageview"]

    @property
    def entry(self) -> str:
        pages = self.pages
        return pages[0].path if pages else ""

    @property
    def exit(self) -> str:
        pages = self.pages
        return pages[-1].path if pages else ""

    @property
    def duration_ms(self) -> int:
        if not self.events:
            return 0
        return round((self.events[-1].ts - self.events[0].ts).total_seconds() * 1000)

    @property
    def bounce(self) -> bool:
        return len(self.pages) == 1 and not self.customs

    def dimension(self, name: str) -> str:
        if name in SESSION_DIMENSIONS:
            return getattr(self, name)
        raise ValueError(f"unsupported session dimension {name!r}")


class Reference:
    def __init__(self, sessions: Iterable[Session]):
        self.sessions = list(sessions)

    @classmethod
    def from_files(cls, events_path: Path | str, sites_path: Path | str) -> "Reference":
        with open(sites_path, encoding="utf-8") as handle:
            sites = {int(site["id"]): site for site in json.load(handle)}
        sessions: dict[tuple[int, str], Session] = {}
        with open(events_path, encoding="utf-8") as handle:
            for line_number, line in enumerate(handle, 1):
                if not line.strip():
                    continue
                raw = json.loads(line)
                site_id = int(raw["site"])
                site = sites.get(site_id)
                if site is None:
                    raise ValueError(f"line {line_number}: unknown site {site_id}")
                ts = datetime.fromisoformat(raw["ts"].replace("Z", "+00:00"))
                day = ts.astimezone(ZoneInfo(site["timezone"])).date().isoformat()
                key = (site_id, raw["sk"])
                session = sessions.get(key)
                if session is None:
                    source, medium = _attribution(raw, site["host"])
                    session = Session(
                        site=site_id,
                        day=day,
                        visitor=raw["vk"],
                        key=raw["sk"],
                        source=source,
                        utm_source=raw.get("utm_source", ""),
                        utm_medium=medium,
                        utm_campaign=raw.get("utm_campaign", ""),
                        country=raw["country"],
                        device=raw["device"],
                        browser=raw["browser"],
                        os=raw["os"],
                    )
                    sessions[key] = session
                elif session.day != day:
                    raise ValueError(f"line {line_number}: session crosses a site-local day")
                session.events.append(
                    Event(ts=ts, name=raw["name"], path=raw["path"], props=raw.get("props", {}), raw=raw)
                )
        return cls(sessions.values())

    def query(
        self,
        site_id: int,
        date_from: str,
        date_to: str,
        *,
        dimensions: list[str] | None = None,
        metrics: list[str] | None = None,
        filters: list[dict[str, Any]] | None = None,
        limit: int = 1000,
    ) -> dict[str, Any]:
        dimensions = dimensions or []
        metrics = metrics or list(DEFAULT_METRICS)
        filters = filters or []
        sessions = [
            session
            for session in self.sessions
            if session.site == site_id and date_from <= session.day <= date_to and _session_matches(session, filters)
        ]
        if dimensions and any(dimension in {"event", "goal"} or dimension.startswith("prop:") for dimension in dimensions):
            rows = self._custom_rows(sessions, dimensions, metrics, filters)
            totals = _session_metrics(sessions, metrics)
        else:
            totals = _session_metrics(sessions, metrics)
            rows = self._session_rows(sessions, dimensions, metrics)
        rows.sort(key=lambda row: _sort_key(row, dimensions, metrics))
        return {"totals": totals, "rows": rows[:limit]}

    @staticmethod
    def _session_rows(sessions: list[Session], dimensions: list[str], metrics: list[str]) -> list[dict[str, Any]]:
        if not dimensions:
            return []
        grouped: dict[tuple[str, ...], list[Session]] = defaultdict(list)
        page_dimension = "page" in dimensions or "path" in dimensions
        for session in sessions:
            if page_dimension:
                page_counts: dict[str, int] = defaultdict(int)
                for event in session.pages:
                    page_counts[event.path] += 1
                for page, count in page_counts.items():
                    key = tuple(page if dimension in {"page", "path"} else session.dimension(dimension) for dimension in dimensions)
                    for _ in range(count):
                        grouped[key].append(session)
            else:
                key = tuple(session.dimension(dimension) for dimension in dimensions)
                if any(dimension.startswith("utm_") and not value for dimension, value in zip(dimensions, key)):
                    continue
                grouped[key].append(session)
        result = []
        for key, grouped_sessions in grouped.items():
            unique_sessions = list({session.key: session for session in grouped_sessions}.values())
            values = _session_metrics(unique_sessions, metrics, page_occurrences=len(grouped_sessions) if page_dimension else None)
            result.append(dict(zip(dimensions, key)) | values)
        return result

    @staticmethod
    def _custom_rows(
        sessions: list[Session], dimensions: list[str], metrics: list[str], filters: list[dict[str, Any]]
    ) -> list[dict[str, Any]]:
        grouped: dict[tuple[str, ...], list[tuple[Session, Event]]] = defaultdict(list)
        for session in sessions:
            for event in session.customs:
                if not _custom_matches(session, event, filters):
                    continue
                values = []
                for dimension in dimensions:
                    if dimension in {"event", "goal"}:
                        values.append(event.name)
                    elif dimension.startswith("prop:"):
                        values.append(event.props.get(dimension[5:], ""))
                    elif dimension in {"page", "path"}:
                        values.append(event.path)
                    else:
                        values.append(session.dimension(dimension))
                if all(values):
                    grouped[tuple(values)].append((session, event))
        return [dict(zip(dimensions, key)) | _custom_metrics(events, metrics) for key, events in grouped.items()]


def _attribution(raw: dict[str, Any], host: str) -> tuple[str, str]:
    if raw.get("utm_source"):
        source, default_medium = _canonical_source(raw["utm_source"])
        raw_medium = raw.get("utm_medium", "").strip().lower()
        return source, MEDIUM_ALIASES.get(raw_medium, raw.get("utm_medium", "")) if raw_medium else default_medium
    referrer = raw.get("ref", "")
    if not referrer:
        return "(direct)", ""
    parsed = urlsplit(referrer)
    ref_host = _normal_host(parsed.hostname or "")
    if not ref_host or ref_host == _normal_host(host):
        return "(direct)", ""
    source, medium = _canonical_source(ref_host)
    return source, medium or "referral"


def _canonical_source(value: str) -> tuple[str, str]:
    token = value.strip().lower()
    host = _normal_host(value)
    for name, medium, domains, aliases in SOURCE_RULES:
        if token in aliases or any(_host_matches(host, domain) for domain in domains):
            return name, medium
    return value, ""


def _normal_host(value: str) -> str:
    value = value.rstrip(".").lower()
    return value[4:] if value.startswith("www.") else value


def _host_matches(host: str, pattern: str) -> bool:
    if pattern.endswith(".*"):
        name = pattern[:-2]
        return host.startswith(name + ".") or ("." + name + ".") in host
    return host == pattern or host.endswith("." + pattern)


def _matches(value: str, filter_: dict[str, Any]) -> bool:
    values = [str(item) for item in filter_["values"]]
    operator = filter_["operator"]
    if operator == "is":
        return value in values
    if operator == "is_not":
        return value not in values
    if operator == "contains":
        return any(item in value for item in values)
    if operator == "starts_with":
        return any(value.startswith(item) for item in values)
    raise ValueError(f"unsupported filter operator {operator!r}")


def _session_matches(session: Session, filters: list[dict[str, Any]]) -> bool:
    for filter_ in filters:
        dimension = filter_["dimension"]
        if dimension in SESSION_DIMENSIONS:
            if not _matches(session.dimension(dimension), filter_):
                return False
        elif dimension in {"page", "path"}:
            if not any(_matches(event.path, filter_) for event in session.pages):
                return False
        elif dimension in {"event", "goal"}:
            if not any(_matches(event.name, filter_) for event in session.customs):
                return False
        elif dimension.startswith("prop:"):
            key = dimension[5:]
            if not any(key in event.props and _matches(event.props[key], filter_) for event in session.customs):
                return False
        else:
            raise ValueError(f"unsupported filter dimension {dimension!r}")
    return True


def _custom_matches(session: Session, event: Event, filters: list[dict[str, Any]]) -> bool:
    for filter_ in filters:
        dimension = filter_["dimension"]
        if dimension in SESSION_DIMENSIONS and not _matches(session.dimension(dimension), filter_):
            return False
        if dimension in {"event", "goal"} and not _matches(event.name, filter_):
            return False
        if dimension.startswith("prop:"):
            key = dimension[5:]
            if key not in event.props or not _matches(event.props[key], filter_):
                return False
    return True


def _session_metrics(sessions: list[Session], metrics: list[str], page_occurrences: int | None = None) -> dict[str, float]:
    visitors = len({(session.day, session.visitor) for session in sessions})
    visits = len(sessions)
    pageviews = page_occurrences if page_occurrences is not None else sum(len(session.pages) for session in sessions)
    bounces = sum(session.bounce for session in sessions)
    duration_ms = sum(session.duration_ms for session in sessions)
    customs = sum(len(session.customs) for session in sessions)
    values = {
        "visitors": float(visitors),
        "visits": float(visits),
        "pageviews": float(pageviews),
        "views_per_visit": _ratio(pageviews, visits),
        "bounce_rate": _percentage(bounces, visits),
        "visit_duration": _ratio(duration_ms, visits) / 1000,
        "events": float(customs),
        "conversions": 0.0,
        "conversion_rate": 0.0,
    }
    return {metric: values[metric] for metric in metrics}


def _custom_metrics(events: list[tuple[Session, Event]], metrics: list[str]) -> dict[str, float]:
    visitors = len({(session.day, session.visitor) for session, _ in events})
    visits = len({session.key for session, _ in events})
    values = {
        "visitors": float(visitors),
        "visits": float(visits),
        "pageviews": 0.0,
        "views_per_visit": 0.0,
        "bounce_rate": 0.0,
        "visit_duration": 0.0,
        "events": float(len(events)),
        "conversions": float(visitors),
        "conversion_rate": _percentage(visitors, visitors),
    }
    return {metric: values[metric] for metric in metrics}


def _ratio(numerator: int, denominator: int) -> float:
    return 0.0 if denominator == 0 else numerator / denominator


def _percentage(numerator: int, denominator: int) -> float:
    return _ratio(numerator, denominator) * 100


def _sort_key(row: dict[str, Any], dimensions: list[str], metrics: list[str]) -> tuple[Any, ...]:
    metric = metrics[0] if metrics else "visitors"
    return (-float(row.get(metric, 0)), *(str(row.get(dimension, "")) for dimension in dimensions))


def compare_response(
    phase: str, query: dict[str, Any], expected: dict[str, Any], actual: dict[str, Any]
) -> list[dict[str, Any]]:
    mismatches: list[dict[str, Any]] = []
    dimensions = list(query.get("dimensions", []))
    for metric, want in expected["totals"].items():
        got = actual.get("totals", {}).get(metric)
        if not _equal(got, want):
            mismatches.append(_mismatch(phase, query, f"totals.{metric}", want, got))

    if not dimensions:
        return mismatches

    def keyed(rows: list[dict[str, Any]]) -> dict[tuple[Any, ...], dict[str, Any]]:
        return {tuple(row.get(dimension) for dimension in dimensions): row for row in rows}

    want_rows = keyed(expected["rows"])
    got_rows = keyed(actual.get("rows", []))
    for key in sorted(set(want_rows) | set(got_rows), key=str):
        if key not in want_rows:
            mismatches.append(_mismatch(phase, query, f"rows.{key}", None, got_rows[key]))
            continue
        if key not in got_rows:
            mismatches.append(_mismatch(phase, query, f"rows.{key}", want_rows[key], None))
            continue
        for metric, want in want_rows[key].items():
            if metric in dimensions:
                continue
            got = got_rows[key].get(metric)
            if not _equal(got, want):
                mismatches.append(_mismatch(phase, query, f"rows.{key}.{metric}", want, got))
    return mismatches


def _equal(actual: Any, expected: Any) -> bool:
    if isinstance(actual, (int, float)) and isinstance(expected, (int, float)):
        return abs(float(actual) - float(expected)) <= 1e-9
    return actual == expected


def _mismatch(phase: str, query: dict[str, Any], metric: str, expected: Any, actual: Any) -> dict[str, Any]:
    return {"phase": phase, "query": query, "metric": metric, "expected": expected, "actual": actual}
