#!/usr/bin/env python3
"""Orchestrate the local accuracy A/B benchmark."""

from __future__ import annotations

import argparse
import dataclasses
import importlib
import json
import os
import socket
import subprocess
import sys
import time
from datetime import datetime, timedelta, timezone
from pathlib import Path
from types import SimpleNamespace
from typing import Any, Callable, Mapping


_ACCURACY_DIR = str(Path(__file__).resolve().parent.parent)
if _ACCURACY_DIR not in sys.path:
    sys.path.insert(0, _ACCURACY_DIR)


@dataclasses.dataclass(frozen=True)
class RunOptions:
    visitors: int = 300
    volume: int = 3_000
    seed: int = 7
    keep: bool = False
    out: Path = Path("tools/bench/results")


def run(options: RunOptions | None = None, deps: Mapping[str, Any] | None = None) -> int:
    """Run both traffic phases and write the three-way comparison reports."""
    options = options or RunOptions()
    dependencies = dict(deps or _default_dependencies())
    output = options.out
    workdir = output / "accuracy-ab"
    workdir.mkdir(parents=True, exist_ok=True)
    stack_path = workdir / "stack.json"
    should_teardown = not options.keep

    try:
        stack_config = dependencies["stack"].up(workdir=workdir)
        if stack_config is None:
            stack_config = json.loads(stack_path.read_text(encoding="utf-8"))
        elif not stack_path.exists():
            _write_private_json(stack_path, stack_config)

        browser = dependencies["scenarios"].generate(options.seed, options.visitors, ip_offset=0)
        browser_path = workdir / "visits.json"
        browser_path.write_text(dependencies["scenarios"].to_json(browser), encoding="utf-8")
        driver_report = workdir / "driver-report.json"
        event_started_at = dependencies["now"]()
        dependencies["driver"](browser_path, driver_report, stack_config)

        volume_scenario = dependencies["scenarios"].generate(
            options.seed + 1,
            options.volume,
            ip_offset=options.visitors,
        )
        volume_path = workdir / "volume-visits.json"
        volume_path.write_text(dependencies["scenarios"].to_json(volume_scenario), encoding="utf-8")
        volume_report = workdir / "volume-report.json"
        dependencies["volume"](volume_path, stack_path, volume_report)
        event_ended_at = dependencies["now"]()

        combined = _combine_scenarios(browser, volume_scenario, options.seed)
        expected = dependencies["truth"](combined)
        date_from, date_to = _utc_date_range(event_started_at, event_ended_at)

        def fetch() -> tuple[dict[str, Any], dict[str, Any]]:
            return (
                dependencies["fetch_supermetry"](stack_config, date_from, date_to),
                dependencies["fetch_plausible"](stack_config, date_from, date_to),
            )

        supermetry, plausible, stable = dependencies["stability"](fetch)
        rows = dependencies["compare"](expected, supermetry, plausible, dependencies["load_known"]())
        meta = {
            "date_from": date_from,
            "date_to": date_to,
            "seed": options.seed,
            "browser_visitors": options.visitors,
            "volume_visitors": options.volume,
            "stability": "STABLE" if stable else "NOT STABLE",
        }
        timestamp = dependencies["now"]().astimezone(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        markdown = dependencies["render_markdown"](rows, meta)
        result_json = dependencies["render_json"](rows, meta)
        (output / f"accuracy-ab-{timestamp}.md").write_text(markdown, encoding="utf-8")
        (output / f"accuracy-ab-{timestamp}.json").write_text(result_json, encoding="utf-8")
        print(markdown, end="")
        return exit_code(rows)
    finally:
        if should_teardown:
            dependencies["stack"].down(state_path=stack_path)


def scenario_date_range(scenarios: list[Any], started_at: datetime) -> tuple[str, str]:
    """Return the inclusive UTC dates containing the first and last event."""
    timestamps = []
    for scenario in scenarios:
        for visit in scenario.visits:
            elapsed = int(visit.start_offset_ms)
            timestamps.append(started_at + timedelta(milliseconds=elapsed))
            for step in visit.steps:
                elapsed += int(getattr(step, "ms", 0))
                timestamps.append(started_at + timedelta(milliseconds=elapsed))
    if not timestamps:
        timestamps.append(started_at)
    return min(timestamps).date().isoformat(), max(timestamps).date().isoformat()


def _utc_date_range(first: datetime, last: datetime) -> tuple[str, str]:
    if first.tzinfo is None or last.tzinfo is None:
        raise ValueError("traffic timestamps must include a timezone")
    first_utc = first.astimezone(timezone.utc)
    last_utc = last.astimezone(timezone.utc)
    return min(first_utc, last_utc).date().isoformat(), max(first_utc, last_utc).date().isoformat()


def wait_until_stable(
    fetch: Callable[[], tuple[dict[str, Any], dict[str, Any]]],
    *,
    stable_for: float = 10,
    timeout: float = 120,
    interval: float = 2,
    monotonic: Callable[[], float] = time.monotonic,
    sleeper: Callable[[float], None] = time.sleep,
) -> tuple[dict[str, Any], dict[str, Any], bool]:
    """Poll until both pageview totals stay unchanged for a bounded period."""
    began = monotonic()
    stable_since = began
    previous: tuple[Any, Any] | None = None
    latest: tuple[dict[str, Any], dict[str, Any]] = ({}, {})
    while True:
        latest = fetch()
        current = (latest[0].get("pageviews"), latest[1].get("pageviews"))
        now = monotonic()
        if current != previous:
            previous = current
            stable_since = now
        elif now - stable_since >= stable_for:
            return latest[0], latest[1], True
        if now - began >= timeout:
            return latest[0], latest[1], False
        sleeper(min(interval, max(timeout - (now - began), 0)))


def exit_code(rows: list[dict[str, Any]]) -> int:
    return 1 if any(row.get("status") in {"UNEXPLAINED", "BROKEN"} for row in rows) else 0


def run_driver(visits: Path, report: Path, stack: dict[str, Any]) -> None:
    driver_dir = Path(__file__).with_name("driver")
    if not (driver_dir / "node_modules").exists():
        subprocess.run(["bun", "install"], cwd=driver_dir, check=True)
    env = os.environ.copy()
    env.update(
        {
            "PLAUSIBLE_URL": str(stack["plausible"]["url"]),
            "PLAUSIBLE_DOMAIN": str(stack["plausible"]["domain"]),
            "SUPERMETRY_SCRIPT_URL": str(stack["supermetry"]["script_url"]),
            "SITE_PORT": str(stack["site"]["port"]),
        }
    )
    site = subprocess.Popen(
        ["bun", "run", "../site/server.ts"],
        cwd=driver_dir,
        env=env,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.STDOUT,
    )
    try:
        _wait_for_site(site, int(stack["site"]["port"]))
        subprocess.run(
            ["bun", "run", "run.ts", "--visits", str(visits.resolve()), "--out", str(report.resolve())],
            cwd=driver_dir,
            env=env,
            check=True,
        )
    finally:
        if site.poll() is None:
            site.terminate()
            try:
                site.wait(timeout=5)
            except subprocess.TimeoutExpired:
                site.kill()
                site.wait(timeout=5)


def _wait_for_site(process: subprocess.Popen[Any], port: int, timeout: float = 10) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError("accuracy test site exited before it became ready")
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.25):
                return
        except OSError:
            time.sleep(0.1)
    raise TimeoutError(f"accuracy test site did not listen on port {port} within {timeout:g} seconds")


def _default_dependencies() -> dict[str, Any]:
    scenarios = importlib.import_module("ab.scenarios")
    truth_module = importlib.import_module("ab.truth")
    stack = importlib.import_module("ab.stack")
    volume = importlib.import_module("ab.volume")
    compare3 = importlib.import_module("ab.compare3")
    return {
        "stack": stack,
        "scenarios": scenarios,
        "truth": truth_module.truth,
        "driver": run_driver,
        "volume": lambda scenario, stack_path, report: _run_volume(volume, scenario, stack_path, report),
        "fetch_supermetry": compare3.fetch_supermetry,
        "fetch_plausible": compare3.fetch_plausible,
        "stability": wait_until_stable,
        "compare": compare3.compare,
        "render_markdown": compare3.render_markdown,
        "render_json": compare3.render_json,
        "load_known": compare3.load_known_differences,
        "now": lambda: datetime.now(timezone.utc),
    }


def _run_volume(module: Any, scenario: Path, stack_path: Path, report: Path) -> None:
    code = module.main(
        ["--scenario", str(scenario), "--stack", str(stack_path), "--report", str(report)]
    )
    if code not in {None, 0}:
        raise RuntimeError(f"volume run failed with exit code {code}")


def _combine_scenarios(first: Any, second: Any, seed: int) -> Any:
    visits = tuple(first.visits) + tuple(second.visits)
    try:
        return type(first)(seed=seed, visits=visits)
    except TypeError:
        return SimpleNamespace(seed=seed, visits=visits)


def _write_private_json(path: Path, value: dict[str, Any]) -> None:
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as target:
        json.dump(value, target, indent=2, sort_keys=True)
        target.write("\n")


def _parse_args(argv: list[str] | None) -> RunOptions:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--visitors", type=int, default=300)
    parser.add_argument("--volume", type=int, default=3_000)
    parser.add_argument("--seed", type=int, default=7)
    parser.add_argument("--keep", action="store_true")
    parser.add_argument("--out", type=Path, default=Path("tools/bench/results"))
    args = parser.parse_args(argv)
    if args.visitors < 0 or args.volume < 0:
        parser.error("visitor counts must be non-negative")
    return RunOptions(args.visitors, args.volume, args.seed, args.keep, args.out)


def main(argv: list[str] | None = None) -> int:
    return run(_parse_args(argv))


if __name__ == "__main__":
    sys.exit(main())
