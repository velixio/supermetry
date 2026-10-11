import dataclasses
import json
import runpy
import sys
import tempfile
import unittest
from datetime import datetime, timezone
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

from ab import run


@dataclasses.dataclass(frozen=True)
class Step:
    ms: int


@dataclasses.dataclass(frozen=True)
class Visit:
    start_offset_ms: int
    steps: tuple[Step, ...]


@dataclasses.dataclass(frozen=True)
class Scenario:
    seed: int
    visits: tuple[Visit, ...]


class FakeClock:
    def __init__(self):
        self.value = 0.0

    def monotonic(self):
        return self.value

    def sleep(self, seconds):
        self.value += seconds


class RunTest(unittest.TestCase):
    def test_teardown_runs_after_driver_failure(self):
        calls = []
        fake_stack = SimpleNamespace(
            up=lambda *, workdir: self._stack_config(),
            down=lambda *, state_path: calls.append("down"),
        )
        deps = self._deps(stack=fake_stack, driver=lambda *_args: (_ for _ in ()).throw(RuntimeError("driver failed")))

        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(RuntimeError, "driver failed"):
                run.run(run.RunOptions(visitors=1, volume=1, out=Path(directory)), deps)

        self.assertEqual(calls, ["down"])

    def test_date_range_includes_an_event_after_utc_midnight(self):
        scenario = Scenario(seed=1, visits=(Visit(start_offset_ms=500, steps=(Step(ms=1_500),)),))

        date_from, date_to = run.scenario_date_range(
            [scenario], datetime(2026, 10, 10, 23, 59, 59, tzinfo=timezone.utc)
        )

        self.assertEqual((date_from, date_to), ("2026-10-10", "2026-10-11"))

    def test_run_queries_the_actual_traffic_window_across_midnight(self):
        times = iter(
            (
                datetime(2026, 10, 10, 23, 59, 59, tzinfo=timezone.utc),
                datetime(2026, 10, 11, 0, 0, 1, tzinfo=timezone.utc),
                datetime(2026, 10, 11, 0, 0, 2, tzinfo=timezone.utc),
            )
        )
        queried = []

        def fetch(_stack, date_from, date_to):
            queried.append((date_from, date_to))
            return {"pageviews": 2}

        deps = self._deps(now=lambda: next(times), fetch_supermetry=fetch, fetch_plausible=fetch)
        with tempfile.TemporaryDirectory() as directory:
            code = run.run(run.RunOptions(visitors=1, volume=1, out=Path(directory)), deps)

        self.assertEqual(code, 0)
        self.assertEqual(queried, [("2026-10-10", "2026-10-11")] * 2)

    def test_wait_until_stable_is_bounded_and_reports_not_stable(self):
        clock = FakeClock()
        count = 0

        def fetch():
            nonlocal count
            count += 1
            return ({"pageviews": count}, {"pageviews": count})

        supermetry, plausible, stable = run.wait_until_stable(
            fetch,
            stable_for=10,
            timeout=12,
            interval=2,
            monotonic=clock.monotonic,
            sleeper=clock.sleep,
        )

        self.assertFalse(stable)
        self.assertEqual(supermetry["pageviews"], plausible["pageviews"])
        self.assertGreaterEqual(clock.value, 12)

    def test_run_writes_not_stable_and_returns_failure_for_unexplained_rows(self):
        def compare(_truth, _supermetry, _plausible, _known):
            return [{"metric": "events", "status": "UNEXPLAINED"}]

        deps = self._deps(compare=compare, stability=lambda _fetch, probe=None: ({"pageviews": 2}, {"pageviews": 2}, False))
        with tempfile.TemporaryDirectory() as directory:
            code = run.run(run.RunOptions(visitors=1, volume=1, out=Path(directory)), deps)
            reports = list(Path(directory).glob("accuracy-ab-*.json"))
            payload = json.loads(reports[0].read_text())

        self.assertEqual(code, 1)
        self.assertEqual(payload["meta"]["stability"], "NOT STABLE")

    def test_exit_code_is_zero_only_without_unexplained_or_broken_rows(self):
        self.assertEqual(run.exit_code([{"status": "MATCH"}, {"status": "EXPLAINED"}]), 0)
        self.assertEqual(run.exit_code([{"status": "UNEXPLAINED"}]), 1)
        self.assertEqual(run.exit_code([{"status": "BROKEN"}]), 1)

    def test_driver_starts_and_stops_the_test_site(self):
        process = mock.Mock()
        process.poll.return_value = None
        stack = self._stack_config()
        with (
            mock.patch("ab.run.subprocess.Popen", return_value=process) as popen,
            mock.patch("ab.run.subprocess.run") as execute,
            mock.patch("ab.run._wait_for_site") as wait_for_site,
            mock.patch("ab.run.Path.exists", return_value=True),
        ):
            run.run_driver(Path("visits.json"), Path("report.json"), stack)

        self.assertEqual(popen.call_args.args[0], ["bun", "run", "../site/server.ts"])
        wait_for_site.assert_called_once_with(process, 18091)
        self.assertEqual(execute.call_args.args[0][:3], ["bun", "run", "run.ts"])
        process.terminate.assert_called_once_with()
        process.wait.assert_called_once_with(timeout=5)

    def test_direct_script_execution_adds_accuracy_package_parent(self):
        script = Path(run.__file__).resolve()
        package_parent = str(script.parent.parent)
        isolated_path = [entry for entry in sys.path if entry != package_parent]

        with mock.patch.object(sys, "path", isolated_path):
            runpy.run_path(str(script), run_name="accuracy_ab_import_check")
            self.assertIn(package_parent, sys.path)

    def _deps(self, **overrides):
        generated = []

        def generate(seed, visitors, ip_offset=0):
            generated.append((seed, visitors, ip_offset))
            return Scenario(seed=seed, visits=(Visit(start_offset_ms=0, steps=(Step(ms=0),)),) * visitors)

        fake_stack = SimpleNamespace(
            up=lambda *, workdir: self._stack_config(),
            down=lambda *, state_path: None,
        )
        defaults = {
            "stack": fake_stack,
            "scenarios": SimpleNamespace(generate=generate, to_json=lambda _scenario: "{}\n"),
            "truth": lambda scenario: {"visitors": len(scenario.visits), "pageviews": 2},
            "driver": lambda *_args: None,
            "volume": lambda *_args: None,
            "fetch_supermetry": lambda *_args: {"pageviews": 2},
            "fetch_plausible": lambda *_args: {"pageviews": 2},
            "stability": lambda fetch, probe=None: (*fetch(), True),
            "compare": lambda *_args: [{"metric": "pageviews", "status": "MATCH"}],
            "render_markdown": lambda _rows, meta: f"stability: {meta['stability']}\n",
            "render_json": lambda rows, meta: json.dumps({"meta": meta, "rows": rows}) + "\n",
            "load_known": lambda: [],
            "now": lambda: datetime(2026, 10, 11, 6, 0, tzinfo=timezone.utc),
        }
        defaults.update(overrides)
        return defaults

    @staticmethod
    def _stack_config():
        return {
            "started_at": "2026-10-10T23:59:59Z",
            "plausible": {"url": "http://127.0.0.1:8000", "domain": "site1.test", "key": "key"},
            "supermetry": {
                "api_url": "http://127.0.0.1:18090",
                "script_url": "http://app.test:18090/js/public.js",
                "key": "key",
                "site_id": 1,
            },
            "site": {"url": "http://app.test:18091", "port": 18091},
        }


if __name__ == "__main__":
    unittest.main()


class ProbeStabilityTests(unittest.TestCase):
    def test_probe_polls_cheaply_and_fetches_once_when_totals_settle(self):
        from ab.run import wait_until_stable

        clock = [0.0]
        probes, fetches = [], []

        def probe():
            probes.append(clock[0])
            return (5, 5) if clock[0] < 4 else (9, 9)

        def fetch():
            fetches.append(clock[0])
            return {"pageviews": 9}, {"pageviews": 9}

        def sleeper(seconds):
            clock[0] += seconds

        result = wait_until_stable(
            fetch, probe=probe, stable_for=6, timeout=60, interval=2, monotonic=lambda: clock[0], sleeper=sleeper
        )
        self.assertEqual(result, ({"pageviews": 9}, {"pageviews": 9}, True))
        self.assertEqual(len(fetches), 1)
        self.assertGreater(len(probes), 3)

    def test_probe_timeout_still_returns_the_data_marked_not_stable(self):
        from ab.run import wait_until_stable

        clock = [0.0]
        counter = [0]

        def probe():
            counter[0] += 1
            return (counter[0], counter[0])

        def sleeper(seconds):
            clock[0] += seconds

        supermetry, plausible, stable = wait_until_stable(
            lambda: ({"pageviews": 1}, {"pageviews": 1}),
            probe=probe, stable_for=10, timeout=12, interval=2, monotonic=lambda: clock[0], sleeper=sleeper,
        )
        self.assertFalse(stable)
        self.assertEqual(supermetry, {"pageviews": 1})
