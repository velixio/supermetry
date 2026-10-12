"""Deterministic live-window fixtures independent of any analytics product."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timedelta, timezone


WINDOW = timedelta(minutes=5)


@dataclass(frozen=True)
class LiveEvent:
    visitor: str
    at: datetime
    accepted: bool = True
    kind: str = "pageview"


def scenario(anchor: datetime | None = None) -> tuple[LiveEvent, ...]:
    """Cover repeats, tabs, SPA activity, filters, shared IP identity, and edges."""
    now = anchor or datetime(2026, 10, 12, 12, 0, 1, tzinfo=timezone.utc)
    return (
        LiveEvent("repeat", now - timedelta(minutes=4, seconds=59)),
        LiveEvent("repeat", now - timedelta(minutes=2)),
        LiveEvent("repeat", now - timedelta(minutes=1), kind="custom"),
        LiveEvent("tabs", now - timedelta(minutes=4)),
        LiveEvent("tabs", now - timedelta(minutes=3), kind="spa"),
        LiveEvent("edge-5m", now - timedelta(minutes=5)),
        LiveEvent("edge-5m01", now - timedelta(minutes=5, seconds=1)),
        LiveEvent("shared-ip-browser-a", now - timedelta(minutes=4)),
        LiveEvent("shared-ip-browser-b", now - timedelta(minutes=4)),
        LiveEvent("bot", now - timedelta(minutes=1), accepted=False),
        LiveEvent("dnt", now - timedelta(minutes=1), accepted=False),
        LiveEvent("gpc", now - timedelta(minutes=1), accepted=False),
        LiveEvent("engagement", now - timedelta(minutes=6)),
        LiveEvent("engagement", now - timedelta(seconds=30), kind="engagement"),
    )


def active_visitors(events: tuple[LiveEvent, ...], at: datetime) -> int:
    """Return independent truth for the exact inclusive five-minute window."""
    return len({
        event.visitor
        for event in events
        if event.accepted
        and event.kind in {"pageview", "custom", "spa"}
        and at - WINDOW <= event.at <= at
    })


def legacy_session_visitors(events: tuple[LiveEvent, ...], at: datetime) -> int:
    """Model the previous bug: any accepted event extended live presence."""
    return len({
        event.visitor
        for event in events
        if event.accepted and at - WINDOW <= event.at <= at
    })


def truth_table(events: tuple[LiveEvent, ...], anchor: datetime) -> list[dict[str, object]]:
    instants = (
        ("anchor", anchor),
        ("after_1s", anchor + timedelta(seconds=1)),
        ("after_61s", anchor + timedelta(seconds=61)),
    )
    return [
        {
            "instant": label,
            "timestamp": at.isoformat(),
            "expected": active_visitors(events, at),
            "legacy_session": legacy_session_visitors(events, at),
        }
        for label, at in instants
    ]

