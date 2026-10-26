"""Deterministic browser-traffic scenarios for the accuracy A/B benchmark."""

from __future__ import annotations

import json
import random
from dataclasses import asdict, dataclass, field
from typing import Any


# These names are the cross-product truth labels. Task 304 may translate them
# when a product deliberately exposes a different display name (Supermetry's
# source table, for example, calls t.co "X/Twitter").
SOURCE_NAMES = {
    "": "(direct)",
    "google": "Google",
    "google.com": "Google",
    "bing": "Bing",
    "bing.com": "Bing",
    "duckduckgo": "DuckDuckGo",
    "duckduckgo.com": "DuckDuckGo",
    "t.co": "Twitter",
    "twitter": "Twitter",
    "news.ycombinator.com": "Hacker News",
    "hacker news": "Hacker News",
    "reddit": "Reddit",
    "reddit.com": "Reddit",
    "linkedin": "LinkedIn",
    "linkedin.com": "LinkedIn",
}


@dataclass(frozen=True)
class Step:
    kind: str
    path: str = ""
    ms: int = 0
    name: str = ""
    props: dict[str, str] = field(default_factory=dict)
    revenue: float | None = None
    currency: str = "USD"
    href: str = ""


@dataclass(frozen=True)
class Visit:
    id: str
    ip: str
    user_agent: str
    viewport: tuple[int, int]
    device: str
    browser: str
    os: str
    referrer: str
    utm: dict[str, str]
    steps: tuple[Step, ...]
    start_offset_ms: int


@dataclass(frozen=True)
class Scenario:
    seed: int
    visits: tuple[Visit, ...]


_TRAFFIC_MIX = (
    ["bounce"] * 35
    + ["journey"] * 40
    + ["spa"] * 10
    + ["event"] * 8
    + ["conversion"] * 5
    + ["click"] * 2
)
_DEVICE_MIX = ["desktop"] * 60 + ["mobile"] * 35 + ["tablet"] * 5
_PAGE_PATHS = ("/", "/about", "/pricing", "/blog/post-1", "/missing")
_SPA_PATHS = ("/spa/a", "/spa/b", "/spa/c")
_EVENTS = ("Signup", "Newsletter", "Demo Request")
_CAMPAIGNS = ("launch", "autumn-sale", "newsletter", "brand")


def generate(seed: int, visitors: int, ip_offset: int = 0) -> Scenario:
    """Return a pure, deterministic scenario containing one visit per visitor.

    The category pools encode the requested traffic mix: 35% bounces, 40%
    two-to-four-page journeys, 10% SPA journeys, 8% single-page custom-event
    visits, 5% USD purchases, and 2% outbound/download clickers. Device pools
    similarly encode a 60/35/5 desktop/mobile/tablet split. Partial blocks are
    shuffled so small scenarios remain representative without global RNG state.
    """
    if visitors < 0:
        raise ValueError("visitors must be non-negative")
    if ip_offset < 0:
        raise ValueError("ip_offset must be non-negative")
    # ip_offset shifts the slot used for the IP, the user-agent generation and the
    # visit id, so a second scenario (the volume run) can never reuse an
    # (IP, user agent) pair or an id from the first one.

    rng = random.Random(seed)
    traffic = _mixed_values(rng, _TRAFFIC_MIX, visitors)
    devices = _mixed_values(rng, _DEVICE_MIX, visitors)
    utm_slots = _mixed_values(rng, [True] * 25 + [False] * 75, visitors)
    visits = []
    for index, (category, device, has_utm) in enumerate(zip(traffic, devices, utm_slots, strict=True)):
        slot = index + ip_offset
        user_agent, viewport, browser, operating_system = _identity(rng, device, slot)
        visits.append(
            Visit(
                id=f"visitor-{slot + 1:06d}",
                ip=_ip_address(slot),
                user_agent=user_agent,
                viewport=viewport,
                device=device,
                browser=browser,
                os=operating_system,
                referrer=_referrer(rng),
                utm=_utm(rng) if has_utm else {},
                steps=_steps(rng, category),
                start_offset_ms=rng.randint(0, 10_000),
            )
        )
    return Scenario(seed=seed, visits=tuple(visits))


def to_json(scenario: Scenario) -> str:
    """Encode a scenario as stable, human-readable JSON."""
    return json.dumps(asdict(scenario), allow_nan=False, indent=2, sort_keys=True) + "\n"


def from_json(text: str) -> Scenario:
    """Decode the canonical scenario JSON representation."""
    raw = json.loads(text)
    visits = []
    for item in raw["visits"]:
        steps = tuple(Step(**step) for step in item["steps"])
        visits.append(
            Visit(
                id=item["id"],
                ip=item["ip"],
                user_agent=item["user_agent"],
                viewport=tuple(item["viewport"]),
                device=item["device"],
                browser=item["browser"],
                os=item["os"],
                referrer=item["referrer"],
                utm=dict(item["utm"]),
                steps=steps,
                start_offset_ms=item["start_offset_ms"],
            )
        )
    return Scenario(seed=raw["seed"], visits=tuple(visits))


def _mixed_values(rng: random.Random, mix: list[Any], count: int) -> list[Any]:
    values = (mix * ((count + len(mix) - 1) // len(mix)))[:count]
    rng.shuffle(values)
    return values


def _ip_address(index: int) -> str:
    # Documentation-only ranges prevent benchmark traffic from naming a real
    # client. Reuse is safe only with a different UA, preserving unique pairs.
    address_index = index % 508
    network = "198.51.100" if address_index < 254 else "203.0.113"
    host = address_index + 1 if address_index < 254 else address_index - 253
    return f"{network}.{host}"


def _identity(rng: random.Random, device: str, index: int) -> tuple[str, tuple[int, int], str, str]:
    # The generation number changes whenever an IP is reused, guaranteeing a
    # distinct (IP, UA) visitor identity without leaving the two allowed /24s.
    generation = index // 508
    chrome_version = 124 + generation
    if device == "mobile":
        return (
            "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 "
            f"(KHTML, like Gecko) Chrome/{chrome_version}.0.0.0 Mobile Safari/537.36",
            rng.choice(((360, 800), (393, 873), (412, 915))),
            "Chrome",
            "Android",
        )
    if device == "tablet":
        return (
            "Mozilla/5.0 (iPad; CPU OS 17_4 like Mac OS X) AppleWebKit/605.1.15 "
            f"(KHTML, like Gecko) Version/17.{generation} Mobile/15E148 Safari/604.1",
            rng.choice(((768, 1024), (820, 1180))),
            "Safari",
            "iOS",
        )

    profile = rng.choice(("windows-chrome", "mac-safari", "linux-firefox"))
    if profile == "windows-chrome":
        return (
            "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
            f"(KHTML, like Gecko) Chrome/{chrome_version}.0.0.0 Safari/537.36",
            rng.choice(((1366, 768), (1440, 900), (1920, 1080))),
            "Chrome",
            "Windows",
        )
    if profile == "mac-safari":
        return (
            "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 "
            f"(KHTML, like Gecko) Version/17.{generation} Safari/605.1.15",
            rng.choice(((1440, 900), (1512, 982))),
            "Safari",
            "macOS",
        )
    return (
        f"Mozilla/5.0 (X11; Linux x86_64; rv:{125 + generation}.0) Gecko/20100101 Firefox/{125 + generation}.0",
        rng.choice(((1366, 768), (1536, 864), (1920, 1080))),
        "Firefox",
        "Linux",
    )


def _referrer(rng: random.Random) -> str:
    # Search/direct/social/spam-or-other are weighted 40/30/20/10.
    group = rng.choices(("search", "direct", "social", "other"), weights=(40, 30, 20, 10), k=1)[0]
    if group == "search":
        return rng.choice(
            (
                "https://www.google.com/search?q=analytics",
                "https://www.bing.com/search?q=analytics",
                "https://duckduckgo.com/?q=analytics",
            )
        )
    if group == "direct":
        return ""
    if group == "social":
        return rng.choice(
            (
                "https://t.co/campaign",
                "https://www.reddit.com/r/analytics/",
                "https://www.linkedin.com/feed/",
            )
        )
    return rng.choice(("https://news.ycombinator.com/", "https://spam.example/referral"))


def _utm(rng: random.Random) -> dict[str, str]:
    source, medium = rng.choice(
        (("google", "cpc"), ("reddit", "social"), ("linkedin", "social"), ("newsletter", "email"))
    )
    return {"source": source, "medium": medium, "campaign": rng.choice(_CAMPAIGNS)}


def _steps(rng: random.Random, category: str) -> tuple[Step, ...]:
    landing = rng.choice(_PAGE_PATHS)
    if category == "bounce":
        return (Step("goto", path=landing),)
    if category == "journey":
        count = rng.randint(2, 4)
        return tuple(Step("goto", path=path) for path in rng.sample(_PAGE_PATHS, count))
    if category == "spa":
        count = rng.randint(2, 5)
        paths = [rng.choice(_SPA_PATHS) for _ in range(count)]
        return (Step("goto", path="/spa"), *(Step("spa", path=path) for path in paths))
    if category == "event":
        return (
            Step("goto", path=landing),
            Step("event", name=rng.choice(_EVENTS), props={"plan": rng.choice(("starter", "pro"))}),
        )
    if category == "conversion":
        return (
            Step("goto", path="/pricing"),
            Step(
                "event",
                name="Purchase",
                props={"plan": rng.choice(("starter", "pro"))},
                revenue=round(rng.uniform(9.99, 199.0), 2),
                currency="USD",
            ),
        )
    if rng.choice((True, False)):
        return (Step("goto", path="/"), Step("outbound", href="https://supermetry.com/out"))
    return (Step("goto", path="/"), Step("download", href="/files/report.pdf"))
