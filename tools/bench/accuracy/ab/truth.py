"""Independent truth calculations for generated accuracy scenarios."""

from __future__ import annotations

from collections import Counter, defaultdict
from urllib.parse import urlsplit

from compare_live import _normal_source as normalise_source

from .scenarios import SOURCE_NAMES, Scenario, Visit


_PAGEVIEW_KINDS = {"goto", "spa", "hash"}
_CUSTOM_NAMES = {
    "outbound": "Outbound Link: Click",
    "download": "File Download",
}


def truth(scenario: Scenario) -> dict:
    """Compute product-independent metrics for one-visit-per-visitor traffic."""
    _validate_identities(scenario)

    page_counts: Counter[str] = Counter()
    source_counts: Counter[str] = Counter()
    campaign_visitors: Counter[str] = Counter()
    device_visitors: Counter[str] = Counter()
    browser_visitors: Counter[str] = Counter()
    os_visitors: Counter[str] = Counter()
    custom_events: Counter[str] = Counter()
    revenue: defaultdict[str, float] = defaultdict(float)
    pageviews = 0
    event_count = 0
    plausible_bounces = 0
    supermetry_bounces = 0

    for current in scenario.visits:
        visit_pageviews = 0
        visit_customs = 0
        source_counts[_source(current)] += 1
        campaign = current.utm.get("campaign", "")
        if campaign:
            # Attribution is visit-scoped, so a campaign contributes at most
            # once for this one-visit visitor regardless of later steps.
            campaign_visitors[campaign] += 1
        device_visitors[current.device] += 1
        browser_visitors[current.browser] += 1
        os_visitors[current.os] += 1

        for step in current.steps:
            if step.kind in _PAGEVIEW_KINDS:
                visit_pageviews += 1
                pageviews += 1
                event_count += 1
                page_counts[step.path] += 1
                continue
            if step.kind == "event":
                name = step.name
            else:
                name = _CUSTOM_NAMES.get(step.kind, "")
            if not name:
                continue
            visit_customs += 1
            event_count += 1
            custom_events[name] += 1
            if step.revenue is not None:
                revenue[step.currency] += step.revenue

        # Plausible's accepted benchmark contract defines bounce as exactly one
        # pageview and no custom event. Supermetry uses the same expression in
        # internal/session/memory.go and internal/closer/rollup.go.
        plausible_bounce = visit_pageviews == 1 and visit_customs == 0
        supermetry_bounce = visit_pageviews == 1 and visit_customs == 0
        plausible_bounces += int(plausible_bounce)
        supermetry_bounces += int(supermetry_bounce)

    visits = len(scenario.visits)
    return {
        "visitors": visits,
        "visits": visits,
        "pageviews": pageviews,
        "events": event_count,
        "bounce_rate_plausible": _percentage(plausible_bounces, visits),
        "bounce_rate_supermetry": _percentage(supermetry_bounces, visits),
        "top_pages": _top(page_counts),
        "top_sources": _top(source_counts),
        "utm_campaign": dict(sorted(campaign_visitors.items())),
        "devices": dict(sorted(device_visitors.items())),
        "browsers": dict(sorted(browser_visitors.items())),
        "oses": dict(sorted(os_visitors.items())),
        "custom_events": dict(sorted(custom_events.items())),
        "revenue": {currency: round(total, 2) for currency, total in sorted(revenue.items())},
    }


def _validate_identities(scenario: Scenario) -> None:
    identities: dict[tuple[str, str], str] = {}
    visitor_ids: set[str] = set()
    for current in scenario.visits:
        if current.id in visitor_ids:
            raise ValueError(f"duplicate visitor id {current.id!r}")
        visitor_ids.add(current.id)
        identity = (current.ip, current.user_agent)
        previous = identities.get(identity)
        if previous is not None and previous != current.id:
            raise ValueError(
                f"visitor identity (ip, user_agent) is shared by {previous!r} and {current.id!r}"
            )
        identities[identity] = current.id


def _source(visit: Visit) -> str:
    utm_source = visit.utm.get("source", "")
    if utm_source:
        return normalise_source(SOURCE_NAMES.get(utm_source.lower(), utm_source))
    if not visit.referrer:
        return normalise_source(SOURCE_NAMES[""])
    host = (urlsplit(visit.referrer).hostname or "").lower().removeprefix("www.")
    for domain, name in SOURCE_NAMES.items():
        if domain and (host == domain or host.endswith("." + domain)):
            return normalise_source(name)
    return normalise_source(host or SOURCE_NAMES[""])


def _percentage(numerator: int, denominator: int) -> float:
    if denominator == 0:
        return 0.0
    return numerator * 100.0 / denominator


def _top(counts: Counter[str]) -> list[tuple[str, int]]:
    return sorted(counts.items(), key=lambda item: (-item[1], item[0]))[:10]
