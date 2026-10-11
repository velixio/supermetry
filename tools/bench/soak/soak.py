#!/usr/bin/env python3
"""Run Supermetry's steady-state ingest soak benchmark."""

from __future__ import annotations

import argparse
import base64
import concurrent.futures
import contextlib
import dataclasses
import datetime as dt
import http.cookiejar
import json
import math
import os
import pathlib
import re
import signal
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Iterable, Sequence
from typing import Any


# First-release targets. Change them only with benchmark evidence and review.
RSS_GROWTH_PERCENT_PER_HOUR = 5.0
P99_LIMIT_MS = 100.0
LATENCY_WINDOW_SECONDS = 5 * 60
RSS_WARMUP_SECONDS = 30 * 60
SAMPLE_SECONDS = 60
REPORT_SECONDS = 15 * 60

ROOT = pathlib.Path(__file__).resolve().parents[3]
DEV_COMPOSE = ROOT / "deploy" / "docker-compose.dev-db.yml"
DATABASE = "soak_run"
DATABASE_URL = "postgres://supermetry:supermetry@127.0.0.1:5433/soak_run?sslmode=disable"
LISTEN = "127.0.0.1:18092"
BASE_URL = "http://127.0.0.1:18092"
TEST_EMAIL = "soak@example.test"
TEST_PASSWORD = "Soak-run-2026-long!"


@dataclasses.dataclass(frozen=True)
class Sample:
    elapsed_s: float
    rss_kib: int
    cpu_seconds: float
    goroutines: int | None
    queue_depth: float
    accepted: int
    dropped: int
    invalid: int
    database_bytes: int
    stored: int
    p50_ms: float
    p95_ms: float
    p99_ms: float
    non_2xx: int
    server_5xx: int

    def to_dict(self) -> dict[str, int | float | None]:
        return dataclasses.asdict(self)


@dataclasses.dataclass(frozen=True)
class Verdict:
    passed: bool
    detail: str

    @property
    def status(self) -> str:
        return "PASS" if self.passed else "FAIL"


def percentile(values: Sequence[float], quantile: float) -> float:
    """Return the nearest-rank percentile for a non-empty sequence."""
    if not values:
        raise ValueError("percentile requires at least one value")
    if not 0 < quantile <= 1:
        raise ValueError("quantile must be in (0, 1]")
    ordered = sorted(values)
    rank = max(1, math.ceil(quantile * len(ordered)))
    return ordered[rank - 1]


def slope_per_hour(samples: Sequence[Sample], warmup_seconds: float = RSS_WARMUP_SECONDS) -> float:
    """Return least-squares RSS growth as percent of warm RSS per hour."""
    steady = [sample for sample in samples if sample.elapsed_s >= warmup_seconds]
    if len(steady) < 2:
        return 0.0
    x = [(sample.elapsed_s - steady[0].elapsed_s) / 3600 for sample in steady]
    y = [float(sample.rss_kib) for sample in steady]
    mean_x = sum(x) / len(x)
    mean_y = sum(y) / len(y)
    denominator = sum((value - mean_x) ** 2 for value in x)
    if denominator == 0 or steady[0].rss_kib <= 0:
        return 0.0
    slope_kib_per_hour = sum((a - mean_x) * (b - mean_y) for a, b in zip(x, y)) / denominator
    return slope_kib_per_hour * 100 / steady[0].rss_kib


def parse_sample(line: str) -> Sample:
    """Parse one JSON sample line with useful errors for malformed data."""
    try:
        raw = json.loads(line)
    except json.JSONDecodeError as exc:
        raise ValueError(f"invalid sample JSON: {exc.msg}") from exc
    if not isinstance(raw, dict):
        raise ValueError("sample must be a JSON object")
    fields = {field.name for field in dataclasses.fields(Sample)}
    missing = fields - raw.keys()
    extra = raw.keys() - fields
    if missing:
        raise ValueError(f"sample missing fields: {', '.join(sorted(missing))}")
    if extra:
        raise ValueError(f"sample has unknown fields: {', '.join(sorted(extra))}")
    try:
        return Sample(**raw)
    except (TypeError, ValueError) as exc:
        raise ValueError(f"invalid sample: {exc}") from exc


def downsample(samples: Sequence[Sample], interval_seconds: int = REPORT_SECONDS) -> list[Sample]:
    """Collapse minute samples into report windows without hiding spikes."""
    if interval_seconds <= 0:
        raise ValueError("interval_seconds must be positive")
    buckets: dict[int, list[Sample]] = {}
    for item in sorted(samples, key=lambda value: value.elapsed_s):
        buckets.setdefault(int(item.elapsed_s // interval_seconds), []).append(item)
    result: list[Sample] = []
    for items in buckets.values():
        latest = items[-1]
        goroutines = [item.goroutines for item in items if item.goroutines is not None]
        result.append(
            dataclasses.replace(
                latest,
                rss_kib=max(item.rss_kib for item in items),
                goroutines=max(goroutines) if goroutines else None,
                queue_depth=max(item.queue_depth for item in items),
                p50_ms=max(item.p50_ms for item in items),
                p95_ms=max(item.p95_ms for item in items),
                p99_ms=max(item.p99_ms for item in items),
                non_2xx=sum(item.non_2xx for item in items),
                server_5xx=sum(item.server_5xx for item in items),
            )
        )
    return result


def verdicts(samples: Sequence[Sample], *, accepted: int, stored: int) -> dict[str, Verdict]:
    if not samples:
        raise ValueError("at least one soak sample is required")
    rss_slope = slope_per_hour(samples)
    windows: dict[int, list[float]] = {}
    for item in samples:
        windows.setdefault(int(item.elapsed_s // LATENCY_WINDOW_SECONDS), []).append(item.p99_ms)
    worst_p99 = max(max(values) for values in windows.values())
    five_xx = sum(item.server_5xx for item in samples)
    dropped = samples[-1].dropped
    steady_count = sum(item.elapsed_s >= RSS_WARMUP_SECONDS for item in samples)
    rss_detail = f"{rss_slope:.2f}%/h (limit {RSS_GROWTH_PERCENT_PER_HOUR:.2f}%/h)"
    if steady_count < 2:
        rss_detail += "; short run has fewer than two post-warmup samples"
    return {
        "rss_growth": Verdict(rss_slope <= RSS_GROWTH_PERCENT_PER_HOUR, rss_detail),
        "p99_latency": Verdict(worst_p99 < P99_LIMIT_MS, f"worst 5-minute window {worst_p99:.2f} ms (limit < {P99_LIMIT_MS:.0f} ms)"),
        "server_5xx": Verdict(five_xx == 0, f"{five_xx} responses"),
        "ingest_drops": Verdict(dropped == 0, f"{dropped} drops"),
        "stored_equals_accepted": Verdict(stored == accepted, f"accepted {accepted}, stored {stored}"),
    }


def parse_duration(value: str) -> float:
    match = re.fullmatch(r"([0-9]+(?:\.[0-9]+)?)(ms|s|m|h)", value.strip())
    if not match:
        raise argparse.ArgumentTypeError("duration must use ms, s, m, or h (for example 4h)")
    amount = float(match.group(1))
    factor = {"ms": 0.001, "s": 1.0, "m": 60.0, "h": 3600.0}[match.group(2)]
    if amount <= 0:
        raise argparse.ArgumentTypeError("duration must be positive")
    return amount * factor


class MinuteStats:
    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._latencies_ms: list[float] = []
        self._codes: dict[int, int] = {}

    def record(self, latency_ms: float, status: int) -> None:
        with self._lock:
            self._latencies_ms.append(latency_ms)
            self._codes[status] = self._codes.get(status, 0) + 1

    def snapshot(self) -> tuple[float, float, float, int, int]:
        with self._lock:
            latencies, self._latencies_ms = self._latencies_ms, []
            codes, self._codes = self._codes, {}
        if not latencies:
            return 0.0, 0.0, 0.0, 0, 0
        non_2xx = sum(count for code, count in codes.items() if code < 200 or code >= 300)
        server_5xx = sum(count for code, count in codes.items() if 500 <= code < 600)
        return (
            percentile(latencies, 0.50),
            percentile(latencies, 0.95),
            percentile(latencies, 0.99),
            non_2xx,
            server_5xx,
        )


class GeneratorSource:
    """Loop deterministic Tier-S output without retaining IPs or user agents."""

    def __init__(self) -> None:
        self._process: subprocess.Popen[str] | None = None

    def _start(self) -> None:
        self._process = subprocess.Popen(
            [
                "go",
                "run",
                "./tools/gen",
                "--tier",
                "S",
                "--days",
                "1",
                "--start",
                "2026-09-30",
                "--seed",
                "42",
                "--out",
                "-",
                "--quiet",
            ],
            cwd=ROOT,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            bufsize=1 << 20,
        )

    def next(self) -> dict[str, Any]:
        while True:
            if self._process is None:
                self._start()
            assert self._process.stdout is not None
            line = self._process.stdout.readline()
            if line:
                value = json.loads(line)
                if not isinstance(value, dict):
                    raise RuntimeError("generator emitted a non-object record")
                return value
            code = self._process.wait()
            self._process = None
            if code != 0:
                raise RuntimeError("tools/gen exited unsuccessfully")

    def close(self) -> None:
        if self._process is not None and self._process.poll() is None:
            self._process.terminate()
            with contextlib.suppress(subprocess.TimeoutExpired):
                self._process.wait(timeout=5)
            if self._process.poll() is None:
                self._process.kill()


def event_request(record: dict[str, Any], public_id: str) -> urllib.request.Request:
    query = urllib.parse.urlencode(
        {
            key: record.get(key, "")
            for key in ("utm_source", "utm_medium", "utm_campaign")
            if record.get(key)
        }
    )
    path = str(record.get("path", "/"))
    url = "https://site1.test" + path + (("?" + query) if query else "")
    body = json.dumps(
        {
            "site": public_id,
            "name": record.get("name", "pageview"),
            "url": url,
            "referrer": record.get("ref", ""),
            "screen_width": record.get("screen_w", 0),
            "props": record.get("props") or {},
        },
        separators=(",", ":"),
    ).encode()
    request = urllib.request.Request(
        BASE_URL + "/api/public/v1/events",
        data=body,
        method="POST",
        headers={"Content-Type": "application/json"},
    )
    # These values are used for the request only and are never written to output.
    request.add_header("User-Agent", str(record.get("ua", "")))
    request.add_header("X-Forwarded-For", str(record.get("ip", "")))
    return request


class LoadDriver:
    def __init__(self, public_id: str, rate: int, stats: MinuteStats, concurrency: int = 256) -> None:
        self.public_id = public_id
        self.rate = rate
        self.stats = stats
        self.concurrency = concurrency
        self.source = GeneratorSource()
        self._pending = threading.BoundedSemaphore(concurrency + rate)
        self._local = threading.local()

    def _send(self, record: dict[str, Any], scheduled: float) -> None:
        status = 0
        try:
            opener = getattr(self._local, "opener", None)
            if opener is None:
                opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
                self._local.opener = opener
            with opener.open(event_request(record, self.public_id), timeout=5) as response:
                status = response.status
                response.read()
        except urllib.error.HTTPError as exc:
            status = exc.code
            exc.close()
        except (OSError, urllib.error.URLError):
            status = 0
        finally:
            self.stats.record((time.monotonic() - scheduled) * 1000, status)
            self._pending.release()

    def run(self, duration_seconds: float, stop: threading.Event | None = None) -> None:
        interval = 1 / self.rate
        first = self.source.next()  # Compile and prime the generator before timing starts.
        next_record: dict[str, Any] | None = first
        started = time.monotonic()
        scheduled = started
        deadline = started + duration_seconds
        try:
            with concurrent.futures.ThreadPoolExecutor(max_workers=self.concurrency) as executor:
                while scheduled < deadline and (stop is None or not stop.is_set()):
                    delay = scheduled - time.monotonic()
                    if delay > 0 and stop is not None:
                        if stop.wait(delay):
                            break
                    elif delay > 0:
                        time.sleep(delay)
                    assert next_record is not None
                    if self._pending.acquire(blocking=False):
                        executor.submit(self._send, next_record, scheduled)
                    else:
                        self.stats.record((time.monotonic() - scheduled) * 1000, 0)
                    scheduled += interval
                    if scheduled < deadline:
                        next_record = self.source.next()
        finally:
            self.source.close()


def run_command(args: Sequence[str], *, env: dict[str, str] | None = None, capture: bool = False) -> str:
    result = subprocess.run(
        list(args),
        cwd=ROOT,
        env=env,
        check=True,
        text=True,
        stdout=subprocess.PIPE if capture else subprocess.DEVNULL,
        stderr=subprocess.PIPE if capture else subprocess.DEVNULL,
    )
    return result.stdout if capture else ""


def docker_psql(database: str, sql: str) -> str:
    return run_command(
        ["docker", "exec", "supermetry-dev-postgres", "psql", "-XAt", "-v", "ON_ERROR_STOP=1", "-U", "supermetry", "-d", database, "-c", sql],
        capture=True,
    )


def create_database() -> None:
    run_command(["docker", "compose", "-f", str(DEV_COMPOSE), "up", "-d", "--wait"])
    docker_psql("postgres", f"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='{DATABASE}' AND pid<>pg_backend_pid();")
    run_command(["docker", "exec", "supermetry-dev-postgres", "dropdb", "--if-exists", "--force", "-U", "supermetry", DATABASE])
    run_command(["docker", "exec", "supermetry-dev-postgres", "createdb", "-U", "supermetry", DATABASE])


def drop_database() -> None:
    with contextlib.suppress(subprocess.CalledProcessError):
        docker_psql("postgres", f"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='{DATABASE}' AND pid<>pg_backend_pid();")
    with contextlib.suppress(subprocess.CalledProcessError):
        run_command(["docker", "exec", "supermetry-dev-postgres", "dropdb", "--if-exists", "--force", "-U", "supermetry", DATABASE])


def runtime_env(rate: int) -> tuple[dict[str, str], dict[str, str]]:
    limit = max(10_000, rate * 4)
    settings = {
        "SUPERMETRY_INGEST_RATE_IP": str(limit),
        "SUPERMETRY_INGEST_RATE_SITE": str(limit),
        "SUPERMETRY_INGEST_RATE_GLOBAL": str(limit),
    }
    # A developer's .env must not silently change the benchmark's shipped defaults.
    env = {key: value for key, value in os.environ.items() if not key.startswith("SUPERMETRY_")}
    env.update(
        {
            "SUPERMETRY_DATABASE_URL": DATABASE_URL,
            "SUPERMETRY_SECRET_KEY": base64.b64encode(os.urandom(32)).decode(),
            "SUPERMETRY_BASE_URL": BASE_URL,
            "SUPERMETRY_LISTEN": LISTEN,
            "SUPERMETRY_TRUSTED_PROXIES": "127.0.0.1/32",
            **settings,
        }
    )
    return env, settings


def wait_ready(timeout: float = 30) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(BASE_URL + "/readyz", timeout=1) as response:
                if response.status == 200:
                    return
        except (OSError, urllib.error.URLError):
            pass
        time.sleep(0.2)
    raise RuntimeError("Supermetry did not become ready within 30 seconds")


def json_request(opener: urllib.request.OpenerDirector, path: str, body: dict[str, Any] | None = None, headers: dict[str, str] | None = None) -> Any:
    data = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request(BASE_URL + path, data=data, method="POST" if body is not None else "GET")
    if body is not None:
        request.add_header("Content-Type", "application/json")
    for name, value in (headers or {}).items():
        request.add_header(name, value)
    with opener.open(request, timeout=5) as response:
        payload = response.read()
    return json.loads(payload) if payload else None


def prometheus_metrics() -> dict[str, float]:
    with urllib.request.urlopen(BASE_URL + "/metrics", timeout=2) as response:
        text = response.read().decode()
    result: dict[str, float] = {}
    for line in text.splitlines():
        if line.startswith("#") or not line.strip():
            continue
        name, _, value = line.partition(" ")
        with contextlib.suppress(ValueError):
            result[name] = float(value)
    return result


def process_stats(pid: int) -> tuple[int, float]:
    status = pathlib.Path(f"/proc/{pid}/status").read_text()
    match = re.search(r"^VmRSS:\s+(\d+)\s+kB$", status, re.MULTILINE)
    if not match:
        raise RuntimeError("VmRSS missing from server process status")
    stat = pathlib.Path(f"/proc/{pid}/stat").read_text()
    after_name = stat[stat.rfind(")") + 2 :].split()
    ticks = int(after_name[11]) + int(after_name[12])
    return int(match.group(1)), ticks / os.sysconf("SC_CLK_TCK")


def database_stats() -> tuple[int, int]:
    lines = docker_psql(DATABASE, "SELECT pg_database_size(current_database()); SELECT count(*) FROM events;").splitlines()
    if len(lines) != 2:
        raise RuntimeError("unexpected database sample output")
    return int(lines[0]), int(lines[1])


def collect_sample(pid: int, elapsed_s: float, stats: MinuteStats) -> Sample:
    rss_kib, cpu_seconds = process_stats(pid)
    metrics = prometheus_metrics()
    database_bytes, stored = database_stats()
    p50_ms, p95_ms, p99_ms, non_2xx, server_5xx = stats.snapshot()
    return Sample(
        elapsed_s=elapsed_s,
        rss_kib=rss_kib,
        cpu_seconds=cpu_seconds,
        goroutines=None,  # No runtime goroutine metric or debug endpoint is exposed.
        queue_depth=metrics.get("supermetry_writer_queue_depth", 0),
        accepted=int(metrics.get("supermetry_ingest_accepted_total", 0)),
        dropped=int(metrics.get("supermetry_ingest_dropped_total", 0)),
        invalid=int(metrics.get("supermetry_ingest_invalid_total", 0)),
        database_bytes=database_bytes,
        stored=stored,
        p50_ms=p50_ms,
        p95_ms=p95_ms,
        p99_ms=p99_ms,
        non_2xx=non_2xx,
        server_5xx=server_5xx,
    )


def sample_until_stopped(pid: int, started: float, stats: MinuteStats, stop: threading.Event, samples: list[Sample], errors: list[BaseException]) -> None:
    next_at = started + SAMPLE_SECONDS
    try:
        while not stop.wait(max(0, next_at - time.monotonic())):
            samples.append(collect_sample(pid, time.monotonic() - started, stats))
            next_at += SAMPLE_SECONDS
    except BaseException as exc:  # Preserve sampler failures for the controlling thread.
        errors.append(exc)
        stop.set()


def wait_for_drain(timeout: float = 60) -> dict[str, float]:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        metrics = prometheus_metrics()
        if metrics.get("supermetry_writer_queue_depth", 0) == 0:
            return metrics
        time.sleep(0.25)
    raise RuntimeError("writer queue did not drain within 60 seconds")


def stop_server(process: subprocess.Popen[bytes], timeout: float = 15) -> None:
    if process.poll() is not None:
        return
    process.send_signal(signal.SIGTERM)
    try:
        process.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


def format_bytes(value: int) -> str:
    return f"{value / (1024 * 1024):.1f} MiB"


def render_report(duration_s: float, rate: int, settings: dict[str, str], samples: Sequence[Sample], results: dict[str, Verdict], accepted: int, stored: int) -> str:
    lines = [
        "# Supermetry soak benchmark",
        "",
        f"- Duration: {duration_s:.0f} seconds",
        f"- Target rate: {rate} events/second",
        "- Traffic: tools/gen tier S, seed 42, looped open-loop",
        f"- Accepted/stored: {accepted}/{stored}",
        "- Goroutines: unavailable (the service exposes neither a runtime metric nor a debug endpoint)",
        "- Ingest settings: " + ", ".join(f"`{key}={value}`" for key, value in sorted(settings.items())),
        "",
        "## Verdicts",
        "",
        "| Check | Result | Detail |",
        "|---|---:|---|",
    ]
    labels = {
        "rss_growth": "RSS growth after 30-minute warmup",
        "p99_latency": "p99 latency in every 5-minute window",
        "server_5xx": "Server 5xx responses",
        "ingest_drops": "Ingest drops",
        "stored_equals_accepted": "Stored equals accepted",
    }
    for key, result in results.items():
        lines.append(f"| {labels[key]} | **{result.status}** | {result.detail} |")
    lines.extend(
        [
            "",
            "## Samples",
            "",
            "Rows combine 15-minute windows; latency, RSS, queue depth, and goroutines retain the window maximum.",
            "",
            "| Elapsed | RSS | CPU s | Goroutines | Queue | Accepted | Drops | Invalid | DB size | Stored | p50 ms | p95 ms | p99 ms | Non-2xx | 5xx |",
            "|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|",
        ]
    )
    for item in downsample(samples):
        goroutines = "—" if item.goroutines is None else str(item.goroutines)
        lines.append(
            f"| {item.elapsed_s / 60:.0f}m | {item.rss_kib / 1024:.1f} MiB | {item.cpu_seconds:.1f} | {goroutines} | "
            f"{item.queue_depth:.0f} | {item.accepted} | {item.dropped} | {item.invalid} | {format_bytes(item.database_bytes)} | "
            f"{item.stored} | {item.p50_ms:.2f} | {item.p95_ms:.2f} | {item.p99_ms:.2f} | {item.non_2xx} | {item.server_5xx} |"
        )
    lines.append("")
    return "\n".join(lines)


def run_soak(duration_s: float, rate: int, output_dir: pathlib.Path) -> pathlib.Path:
    if rate <= 0:
        raise ValueError("rate must be positive")
    output_dir.mkdir(parents=True, exist_ok=True)
    env, settings = runtime_env(rate)
    server: subprocess.Popen[bytes] | None = None
    server_log: Any | None = None
    temporary = tempfile.TemporaryDirectory(prefix="supermetry-soak-")
    tempdir = pathlib.Path(temporary.name)
    samples: list[Sample] = []
    sampler_errors: list[BaseException] = []
    stop = threading.Event()
    stats = MinuteStats()
    try:
        create_database()
        binary = tempdir / "supermetry"
        run_command(["go", "build", "-o", str(binary), "./cmd/supermetry"])
        reset_url = run_command([str(binary), "user", "create", "--email", TEST_EMAIL, "--org", "Soak benchmark"], env=env, capture=True).strip()
        server_log = (tempdir / "server.log").open("wb")
        server = subprocess.Popen([str(binary), "serve"], cwd=ROOT, env=env, stdout=server_log, stderr=subprocess.STDOUT)
        wait_ready()

        # Complete the setup flow using the reset link created before startup.
        token = urllib.parse.parse_qs(urllib.parse.urlparse(reset_url).query).get("token", [""])[0]
        if not token:
            raise RuntimeError("user create did not return a password-reset token")
        jar = http.cookiejar.CookieJar()
        opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
        json_request(opener, "/api/private/auth/password-reset/confirm", {"token": token, "password": TEST_PASSWORD})
        json_request(opener, "/api/private/auth/login", {"email": TEST_EMAIL, "password": TEST_PASSWORD, "device_label": "Soak benchmark"})
        csrf = next((cookie.value for cookie in jar if cookie.name == "supermetry_csrf"), "")
        organizations = json_request(opener, "/api/private/orgs")
        memberships = organizations.get("data", []) if isinstance(organizations, dict) else []
        if not csrf or not memberships:
            raise RuntimeError("could not authenticate benchmark owner")
        org_id = memberships[0].get("org_id", memberships[0].get("OrgID"))
        site = json_request(
            opener,
            "/api/private/sites",
            {"name": "Soak", "hosts": ["site1.test"], "timezone": "UTC", "retention_days": 30, "revenue_currency": "USD"},
            {"X-CSRF-Token": csrf, "X-Supermetry-Org": str(org_id)},
        )
        public_id = site.get("public_id", site.get("PublicID")) if isinstance(site, dict) else None
        if not public_id:
            raise RuntimeError("site response did not include public_id")

        started = time.monotonic()
        sampler = threading.Thread(target=sample_until_stopped, args=(server.pid, started, stats, stop, samples, sampler_errors), daemon=True)
        sampler.start()
        LoadDriver(str(public_id), rate, stats).run(duration_s, stop)
        stop.set()
        sampler.join(timeout=10)
        if sampler_errors:
            raise RuntimeError(f"sampling failed: {sampler_errors[0]}")
        elapsed = time.monotonic() - started
        if not samples or elapsed - samples[-1].elapsed_s > 1:
            samples.append(collect_sample(server.pid, elapsed, stats))
        final_metrics = wait_for_drain()
        accepted = int(final_metrics.get("supermetry_ingest_accepted_total", 0))
        stop_server(server)
        server = None
        _, stored = database_stats()
        results = verdicts(samples, accepted=accepted, stored=stored)
        stamp = dt.datetime.now(dt.UTC).strftime("%Y%m%dT%H%M%SZ")
        report = output_dir / f"soak-{stamp}.md"
        report.write_text(render_report(duration_s, rate, settings, samples, results, accepted, stored))
        if not all(result.passed for result in results.values()):
            raise RuntimeError(f"soak thresholds failed; see {report}")
        return report
    finally:
        stop.set()
        if server is not None:
            stop_server(server)
        if server_log is not None:
            server_log.close()
        drop_database()
        temporary.cleanup()


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--duration", default="4h", type=parse_duration)
    parser.add_argument("--rate", default=200, type=int)
    parser.add_argument("--out", default="tools/bench/results", type=pathlib.Path)
    parser.add_argument("--smoke", action="store_true", help="run for 2 minutes at 50 events/second")
    args = parser.parse_args(argv)
    duration = 2 * 60 if args.smoke else args.duration
    rate = 50 if args.smoke else args.rate
    try:
        report = run_soak(duration, rate, args.out)
    except (OSError, RuntimeError, ValueError, subprocess.CalledProcessError, urllib.error.URLError) as exc:
        print(f"soak: {exc}", file=sys.stderr)
        return 1
    print(report)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
